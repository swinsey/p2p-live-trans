package main

//#cgo LDFLAGS: -L./ -ltragonhls -Wl,-rpath=./
//#include <stdlib.h>
//#include <stdio.h>
//#include "MpegExport.h"
import "C"

import (
	"container/list"
	"fmt"
	"log"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/SkyGlance/kcp-tunnel"
	"github.com/SkyGlance/myhttp"

	jsoniter "github.com/json-iterator/go"
	"github.com/pkg/errors"
)

type segmentdata struct {
	vbuf       []byte
	duration   int
	startdts   int64
	enddts     int64
	segurl     string
	segplayurl string
	indexsec   uint32
}

type uploaders struct {
	upinfo      uploadinfo
	orgremote   string
	remote      string   //for none tunnel
	remoteaddrs []string //for udp tunnel

	basepathurl string
	uploadurl   string // /test.u3u8

	// ptr *segmentdata
	bufmux   sync.Mutex
	buflist  *list.List
	sendlist *list.List
	dellist  *list.List

	upload_fregment_num         int64
	upload_failed_fregment      int64
	upload_fregment_list        int64
	upload_failed_fregment_list int64

	delete_failed_num int64
	delete_num        int64

	upload_duration_max int
	upload_duration_min int
	upload_duration_avg int

	die     chan struct{}
	bclosed bool
}

type HlsSegmenter struct {
	channelname string
	seguploader []*uploaders
	psegmenter  unsafe.Pointer

	// ptr *segmentdata
	buflist *list.List

	videoinfo        string
	currentingesturl string
	streamband       uint32
	streambytes      uint64
	streamdurations  uint64

	setduration    int
	freg_window    int
	freg_extwindow int

	baseurl        string
	basepath       string
	playbaseurl    string
	uploadbasepath string
	ingest_urls    []string
	upload_urls    []uploadinfo

	conf *upServerTunnelConfig

	segindex uint32

	busetunnel     bool
	localtuneladdr string
	uptunnel       *tunnel.UpServTunneler

	die chan struct{}
}

type Ingestjson struct {
	Baseurl  string   `json:"channel_url"`
	Duration int32    `json:"seg_duration"`
	Ingester []string `json:"ingest_urls"`
}

type ChanInfo struct {
	Name           string `json:"channel_name"`
	Bandwidth      string `json:"stream_bandwidth"`
	Streaminfo     string `json:"stream_info"`
	Curingest      string `json:"current_ingesturl"`
	StreamBytes    string `json:"stream_bytes"`
	StreamDuration string `json:"stream_duration"`
}

func (hs *HlsSegmenter) calcduration(blocal bool, startm time.Time) time.Duration {
	duration := time.Since(startm)
	// dest := ""
	// if blocal {
	// 	dest = "local"
	// } else {
	// 	dest = "remote"
	// }
	// log.Printf("%s duration %v", dest, duration)
	return duration
}

func (hs *HlsSegmenter) rundelurl(ptask *uploaders, url string) {
	var client *myhttp.MyHttpClient
	log.Printf("New Del task,url:%s remote:%s", url, ptask.remote)
	startm := time.Now()
	var bremote bool
	if hs.busetunnel && len(ptask.remoteaddrs) > 0 {
		hs.uptunnel.SetRemoteAddr(ptask.remote)
		bremote = true
		client = myhttp.NewHttpClient(hs.localtuneladdr, 500*time.Millisecond)
	} else {
		client = myhttp.NewHttpClient(ptask.orgremote, time.Duration(hs.setduration/2)*time.Millisecond)
	}
	defer hs.calcduration(bremote, startm)
	if client == nil {
		log.Printf("address:%s delete:%s connect timeout", ptask.remote, url)
		ptask.upload_failed_fregment++
		return
	}
	defer client.Close()
	client.SetSendHeader("Host", ptask.orgremote)
	client.SetSendHeader("User-Agent", "hls_ingester")
	client.SetSendHeader("Accept:", "*/*")
	client.SetSendHeader("Connection", "close")
	err := client.SendRequestHeader("DELETE", url, time.Duration(hs.setduration/2)*time.Millisecond)
	if err != nil {
		log.Printf("address:%s delete:%s failed,err:%v", ptask.remote, url, err)
		ptask.delete_failed_num++
		return
	}
	header, err := client.RecvHeader(time.Duration(hs.setduration/2) * time.Millisecond)
	if header == nil || err != nil {
		log.Printf("address:%s delete:%s failed,err:%v", ptask.remote, url, err)
		ptask.delete_failed_num++
		return
	}
	statuscode, ok := header["StatusCode"]
	if !ok {
		log.Printf("address:%s delete:%s failed", ptask.remote, url)
		ptask.delete_failed_num++
		return
	}
	nstatus, err := strconv.Atoi(statuscode)
	if err != nil {
		log.Printf("address:%s delete:%s failed", ptask.remote, url)
		ptask.delete_failed_num++
		return
	}
	if nstatus >= 300 || nstatus < 100 {
		log.Printf("address:%s delete:%s status:%d failed", ptask.remote, url, nstatus)
		ptask.delete_failed_num++
		return
	}
	ptask.delete_num++
	log.Printf("Del task,url:%s remote:%s success", url, ptask.remote)
}

func (hs *HlsSegmenter) runposttask(ptask *uploaders, pdata *segmentdata, m3u8str string, maxduration int) {
	var client *myhttp.MyHttpClient
	startm := time.Now()
	var bremote bool
	log.Printf("New Post task,url:%s len:%d remote:%s duration:%d",
		pdata.segurl, len(pdata.vbuf), ptask.remote, pdata.duration)
	if hs.busetunnel && len(ptask.remoteaddrs) > 0 {
		hs.uptunnel.SetRemoteAddr(ptask.remote)
		bremote = true
		client = myhttp.NewHttpClient(hs.localtuneladdr, 500*time.Millisecond)
	} else {
		client = myhttp.NewHttpClient(ptask.remote, time.Duration(pdata.duration/2)*time.Millisecond)
	}
	defer hs.calcduration(bremote, startm)
	if client == nil {
		log.Printf("address:%s post file:%s failed", ptask.remote, pdata.segurl)
		ptask.upload_failed_fregment++
		return
	}
	defer client.Close()
	client.SetSendHeader("Host", ptask.orgremote)
	client.SetSendHeader("Accept-Ranges", "bytes")
	client.SetSendHeader("Content-Type", "video/mp2t")
	client.SetSendHeader("User-Agent", "hls_ingester")
	client.SetSendHeader("Content-Length", fmt.Sprintf("%d", len(pdata.vbuf)))
	client.SetSendHeader("Cache-Control", "no-cache")
	client.SetSendHeader("Accept:", "*/*")
	client.SetSendHeader("Connection", "close")
	err := client.SendRequestHeader("PUT", pdata.segurl, time.Duration(pdata.duration/2)*time.Millisecond)
	if err != nil {
		log.Printf("address:%s post file:%s failed", ptask.remote, pdata.segurl)
		ptask.upload_failed_fregment++
		return
	}
	nsend, err := client.SendData(pdata.vbuf, time.Duration(pdata.duration)*time.Millisecond)
	if nsend < len(pdata.vbuf) || err != nil {
		log.Printf("address:%s post file:%s failed,err:%v", ptask.remote, pdata.segurl, err)
		ptask.upload_failed_fregment++
		return
	}
	//	log.Printf("POST url:%s bodylen:%d complete", pdata.segurl, len(pdata.vbuf))
	header, err := client.RecvHeader(time.Duration(pdata.duration) * time.Millisecond)
	if header == nil || err != nil {
		log.Printf("address:%s post file:%s failed,get server reply header failed",
			ptask.remote, pdata.segurl)
		ptask.upload_failed_fregment++
		return
	}
	statuscode, ok := header["StatusCode"]
	if !ok {
		log.Printf("address:%s post file:%s failed,server no status reply", ptask.remote, pdata.segurl)
		ptask.upload_failed_fregment++
		return
	}
	nstatus, err := strconv.Atoi(statuscode)
	if err != nil {
		log.Printf("address:%s post file:%s failed,server no status reply", ptask.remote, pdata.segurl)
		ptask.upload_failed_fregment++
		return
	}
	if nstatus >= 300 || nstatus < 100 {
		log.Printf("address:%s post file:%s failed,status:%d", ptask.remote, pdata.segurl, nstatus)
		ptask.upload_failed_fregment++
		return
	}
	ptask.upload_fregment_num++
	if ptask.upload_duration_min == 0 {
		ptask.upload_duration_min = pdata.duration
		ptask.upload_duration_avg = pdata.duration
	}
	if ptask.upload_duration_min > pdata.duration {
		ptask.upload_duration_min = pdata.duration
	}
	if ptask.upload_duration_max < pdata.duration {
		ptask.upload_duration_max = pdata.duration
	}
	ptask.upload_duration_avg += pdata.duration
	ptask.upload_duration_avg /= 2
	log.Printf("Post task,url:%s remote:%s success", pdata.segurl, ptask.remote)
	hs.runpostm3u8(ptask, m3u8str, ptask.uploadurl, maxduration)
}

func (hs *HlsSegmenter) runpostm3u8(ptask *uploaders, value, ourl string, timeout int) {
	var client *myhttp.MyHttpClient
	startm := time.Now()
	var bremote bool
	log.Printf("New Post m3u8 task,url:%s remote:%s", ourl, ptask.remote)
	if hs.busetunnel && len(ptask.remoteaddrs) > 0 {
		hs.uptunnel.SetRemoteAddr(ptask.remote)
		bremote = true
		client = myhttp.NewHttpClient(hs.localtuneladdr, 500*time.Millisecond)
	} else {
		client = myhttp.NewHttpClient(ptask.remote, time.Duration(timeout/2)*time.Millisecond)
	}
	defer hs.calcduration(bremote, startm)
	if client == nil {
		log.Printf("address:%s post m3u8:%s failed", ptask.remote, ourl)
		ptask.upload_failed_fregment_list++
		return
	}
	defer client.Close()
	client.SetSendHeader("Host", ptask.orgremote)
	client.SetSendHeader("Accept-Ranges", "bytes")
	client.SetSendHeader("Content-Type", "application/vnd.apple.mpegurl")
	client.SetSendHeader("User-Agent", "hls_ingester")
	client.SetSendHeader("Content-Length", fmt.Sprintf("%d", len(value)))
	client.SetSendHeader("Cache-Control", "no-cache")
	client.SetSendHeader("Accept:", "*/*")
	client.SetSendHeader("Connection", "close")
	err := client.SendRequestHeader("PUT", ourl, time.Duration(timeout/2)*time.Millisecond)
	if err != nil {
		log.Printf("address:%s post m3u8:%s failed", ptask.remote, ourl)
		ptask.upload_failed_fregment_list++
		return
	}
	out := []byte(value)
	nsend, err := client.SendData(out, time.Duration(timeout)*time.Millisecond)
	if nsend < len(value) || err != nil {
		log.Printf("address:%s post m3u8:%s failed", ptask.remote, ourl)
		ptask.upload_failed_fregment_list++
		return
	}
	header, err := client.RecvHeader(time.Duration(timeout) * time.Millisecond)
	if header == nil || err != nil {
		log.Printf("address:%s post m3u8:%s failed,get server reply header failed", ptask.remote, ourl)
		ptask.upload_failed_fregment_list++
		return
	}
	statuscode, ok := header["StatusCode"]
	if !ok {
		log.Printf("address:%s post m3u8:%s failed,server no status reply", ptask.remote, ourl)
		ptask.upload_failed_fregment_list++
		return
	}
	nstatus, err := strconv.Atoi(statuscode)
	if err != nil {
		log.Printf("address:%s post m3u8:%s failed,server no status reply", ptask.remote, ourl)
		ptask.upload_failed_fregment_list++
		return
	}
	if nstatus >= 300 || nstatus < 100 {
		log.Printf("address:%s post m3u8:%s failed,status:%d", ptask.remote, ourl, nstatus)
		ptask.upload_failed_fregment_list++
		return
	}
	ptask.upload_fregment_list++
	log.Printf("Post m3u8 task,url:%s remote:%s success", ourl, ptask.remote)
}

func (hs *HlsSegmenter) uploadertask(ptask *uploaders) {
	duration := time.Duration(hs.setduration/5) * time.Millisecond
	lastindex := strings.LastIndexByte(ptask.basepathurl, '/')
	if lastindex != len(ptask.basepathurl)-1 || lastindex <= 0 {
		log.Fatalf("post base url:%s error", ptask.basepathurl)
		return
	}
	go hs.rundelurl(ptask, ptask.basepathurl)
	//	time.Sleep(3600 * time.Second)
	timer := time.NewTicker(duration)
	defer timer.Stop()
	// make channel have different stages when users downloading segments
	sleeptm := time.Duration(hs.setduration/(rand.Int()%4+2)) * time.Millisecond
	for {
		select {
		case <-timer.C:
			var newseg *segmentdata
			ptask.bufmux.Lock()
			if ptask.buflist.Len() <= 0 {
				ptask.bufmux.Unlock()
				break
			}
			newseg = ptask.buflist.Front().Value.(*segmentdata)
			ptask.buflist.Remove(ptask.buflist.Front())
			ptask.bufmux.Unlock()
			time.Sleep(sleeptm)
			//			log.Printf("new seg duration:%v", newseg.duration)
			if hs.busetunnel && len(ptask.remoteaddrs) > 0 {
				ptask.remote = ptask.remoteaddrs[rand.Int()%len(ptask.remoteaddrs)]
			}
			ptask.sendlist.PushBack(newseg)
			if ptask.sendlist.Len() > hs.freg_window {
				frontseg := ptask.sendlist.Front().Value.(*segmentdata)
				ptask.sendlist.Remove(ptask.sendlist.Front())
				ptask.dellist.PushBack(frontseg)
			}
			if ptask.dellist.Len() > hs.freg_extwindow {
				delseg := ptask.dellist.Front().Value.(*segmentdata)
				ptask.dellist.Remove(ptask.dellist.Front())
				go hs.rundelurl(ptask, delseg.segurl)
			}

			maxduration := 0
			for e := ptask.sendlist.Front(); e != nil; e = e.Next() {
				segvalue := e.Value.(*segmentdata)
				if maxduration < segvalue.duration {
					maxduration = segvalue.duration
				}
			}
			m3u8str := ""
			m3u8str = fmt.Sprintf("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-ALLOW-CACHE:NO\n#EXT-X-TARGETDURATION:%d.%d\n",
				maxduration/1000, maxduration%1000)
			var firstsec bool = true
			for e := ptask.sendlist.Front(); e != nil; e = e.Next() {
				segvalue := e.Value.(*segmentdata)
				if firstsec {
					firstsec = false
					m3u8str += fmt.Sprintf("#EXT-X-MEDIA-SEQUENCE:%d\n", segvalue.indexsec)
				}
				m3u8str += fmt.Sprintf("#EXTINF:%d.%d,\n%s\n",
					segvalue.duration/1000, segvalue.duration%1000, segvalue.segplayurl)
			}
			go hs.runposttask(ptask, newseg, m3u8str, maxduration)
		case <-ptask.die:
			go hs.rundelurl(ptask, ptask.basepathurl)
			return
		}
	}
}

func (hs *HlsSegmenter) looptask() {
	duration := time.Duration(hs.setduration/5) * time.Millisecond
	timer := time.NewTicker(duration)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			segdata := hs.getsegmentdata()
			if segdata != nil {
				hs.segindex++
				segdata.indexsec = hs.segindex
				segdata.segurl = fmt.Sprintf("%s_%d.ts", hs.baseurl, hs.segindex)
				segdata.segplayurl = fmt.Sprintf("%s_%d.ts", hs.playbaseurl, hs.segindex)
				for _, uploader := range hs.seguploader {
					uploader.bufmux.Lock()
					uploader.buflist.PushBack(segdata)
					uploader.bufmux.Unlock()
				}
				hs.buflist.PushBack(segdata)
				hs.streamband = uint32(len(segdata.vbuf) * 1000 / segdata.duration)
				hs.streambytes += uint64(len(segdata.vbuf))
				hs.streamdurations += uint64(segdata.duration)
				if hs.buflist.Len() > hs.freg_window {
					hs.buflist.Remove(hs.buflist.Front())
				}
			}
		case <-hs.die:
			return
		}
	}
}

func (hs *HlsSegmenter) getsegmentdata() *segmentdata {
	if hs.psegmenter == nil {
		return nil
	}
	segdata := C.struct_SegMentData{pcurrenturl: nil, pvideoinfo: nil,
		pbuf: nil, nbuflen: 0, duration: 0, startdts: 0, enddts: 0}
	if C.Segmenter_GetSegment(hs.psegmenter, unsafe.Pointer(&segdata)) < 0 {
		return nil
	}
	psegment := new(segmentdata)
	psegment.duration = int(segdata.duration) / 90
	psegment.startdts = int64(segdata.startdts)
	psegment.enddts = int64(segdata.enddts)
	psegment.vbuf = C.GoBytes(unsafe.Pointer(segdata.pbuf), segdata.nbuflen)
	hs.videoinfo = C.GoString(segdata.pvideoinfo)
	hs.currentingesturl = C.GoString(segdata.pcurrenturl)
	C.Segmenter_FreeSegment(hs.psegmenter, unsafe.Pointer(&segdata))
	return psegment
}

func (hs *HlsSegmenter) GetChannelInfo() (cinfo ChanInfo) {
	cinfo.Bandwidth = fmt.Sprintf("%db/s", hs.streamband)
	cinfo.Curingest = hs.currentingesturl
	cinfo.Streaminfo = hs.videoinfo
	cinfo.StreamBytes = fmt.Sprintf("%dkb", hs.streambytes/1000)
	cinfo.StreamDuration = fmt.Sprintf("%ds", hs.streamdurations/1000)
	return
}

func (hs *HlsSegmenter) CloseSegmenter() {
	for _, upload := range hs.seguploader {
		if upload.bclosed {
			continue
		}
		close(upload.die)
		upload.bclosed = true
	}
	close(hs.die)
	if hs.psegmenter != nil {
		C.Segmenter_Interrupt(hs.psegmenter)
		for {
			if C.Segmenter_Stoped(hs.psegmenter) < 0 {
				time.Sleep(time.Millisecond * 500)
			} else {
				break
			}
		}
		C.Segmenter_Free(hs.psegmenter)
		hs.psegmenter = nil
	}
}

func (hs *HlsSegmenter) UpdateIngestUrls(urls []string) int {
	if len(urls) <= 0 {
		return -1
	}
	if hs.psegmenter == nil {
		ingestcmd := Ingestjson{
			Baseurl:  hs.baseurl,
			Duration: int32(hs.setduration),
			Ingester: urls,
		}

		value, err := jsoniter.Marshal(ingestcmd)
		if err != nil {
			return -1
		}
		strvalue := C.CString(string(value[:]))
		hs.psegmenter = C.Segmenter_Alloc(strvalue)
		if hs.psegmenter == nil {
			C.free(unsafe.Pointer(strvalue))
			return -2
		}
		C.free(unsafe.Pointer(strvalue))
	}
	ingestcmd := Ingestjson{
		Baseurl:  hs.baseurl,
		Duration: int32(hs.setduration),
		Ingester: urls,
	}
	value, err := jsoniter.Marshal(ingestcmd)
	if err != nil {
		return -2
	}
	strvalue := C.CString(string(value[:]))
	defer C.free(unsafe.Pointer(strvalue))
	if C.Segmenter_UpdateIngestUrls(hs.psegmenter, strvalue) < 0 {
		return -3
	}
	return 0
}

func (hs *HlsSegmenter) UpdateWindow(nwindow, extrwindow int) {
	if extrwindow <= 1 {
		extrwindow = 1
	}
	if nwindow <= 2 {
		nwindow = 2
	}
	hs.freg_window = nwindow
	hs.freg_extwindow = extrwindow
}

func (hs *HlsSegmenter) UpdateSegDuration(duration int) int {
	if hs.psegmenter == nil {
		return -1
	}
	if C.Segmenter_UpdateDuration(hs.psegmenter, C.int(duration)) < 0 {
		return -2
	}
	return 0
}

func (hs *HlsSegmenter) UpdatePostServers(upload []uploadinfo) int {
	var newup []*uploaders
	for _, oldva := range hs.seguploader {
		var find bool = false
		for _, newva := range upload {
			if oldva.upinfo.remote == newva.remote && oldva.upinfo.ip == newva.ip &&
				oldva.upinfo.tunnelstartport == newva.tunnelstartport &&
				oldva.upinfo.tunnelendport == newva.tunnelendport {
				find = true
				break
			}
		}
		if !find {
			close(oldva.die)
			oldva.bclosed = true
		} else {
			newup = append(newup, oldva)
		}
	}
	hs.seguploader = newup
	for _, new1 := range upload {
		var find bool = false
		for _, val1 := range hs.seguploader {
			if val1.upinfo.remote == new1.remote && val1.upinfo.ip == new1.ip &&
				val1.upinfo.tunnelstartport == new1.tunnelstartport &&
				val1.upinfo.tunnelendport == new1.tunnelendport {
				find = true
				break
			}
		}
		if !find {
			newup1 := new(uploaders)
			newup1.basepathurl = hs.basepath
			newup1.buflist = list.New()
			newup1.dellist = list.New()
			newup1.sendlist = list.New()
			newup1.remote = new1.remote
			newup1.remoteaddrs = new1.addrs
			newup1.uploadurl = hs.baseurl + ".m3u8"
			newup1.orgremote = new1.remote
			newup1.die = make(chan struct{})
			hs.seguploader = append(hs.seguploader, newup1)
			go hs.uploadertask(newup1)
		}
	}
	return 0
}

func getreplaceindex(url string, substr byte, ncount int) int {
	nindex := 0
	ncheck := 0
	for {
		if url[nindex] == substr {
			ncheck++
		}
		if ncheck >= ncount {
			return nindex
		}
		nindex++
		if nindex >= len(url) {
			return -1
		}
	}
	return 2
}

func replaceurl(url, uploadbase string) string {
	nurl := strings.Count(url, "/")
	nlastup := strings.LastIndex(uploadbase, "/")
	if nlastup >= len(uploadbase)-1 {
		uploadbase = uploadbase[:nlastup-1]
	}
	nup := strings.Count(uploadbase, "/")
	if nup >= nurl || nup < 1 || nurl < 2 || len(url) <= 3 || len(uploadbase) <= 2 {
		return ""
	}
	nlasturl := getreplaceindex(url, '/', nup+1)
	if nlasturl <= 0 {
		return ""
	}
	newstr := uploadbase
	newstr += url[nlasturl:]
	return newstr
}

func mergeslashurl(url string) string {
	index := 0
	newstr := ""
	for {
		if index >= len(url)-1 {
			newstr += string(url[index])
			break
		}
		if url[index] == '/' && url[index+1] == '/' {
			newstr += string(url[index])
			index += 2
		} else {
			newstr += string(url[index])
			index++
		}
	}
	return newstr
}

func (hs *HlsSegmenter) StartSegmenter(tunnel *tunnel.UpServTunneler, conf *upServerTunnelConfig, channelname string, uploadbase string) (int, error) {

	conf.channelurl = mergeslashurl(conf.channelurl)
	baseurl := conf.channelurl
	if conf.duration <= 100 || len(baseurl) <= 3 {
		return -1, errors.New("args error")
	}
	firstindex := strings.Index(baseurl, "/")
	if firstindex != 0 {
		return -1, errors.Errorf("play url:%s invalid", baseurl)
	}
	hs.channelname = channelname
	hs.psegmenter = nil
	if len(conf.ingesturls) > 0 {
		ingestcmd := Ingestjson{
			Baseurl:  baseurl,
			Duration: int32(conf.duration),
			Ingester: conf.ingesturls,
		}

		value, err := jsoniter.Marshal(ingestcmd)
		if err != nil {
			return -1, err
		}
		strvalue := C.CString(string(value[:]))
		hs.psegmenter = C.Segmenter_Alloc(strvalue)
		if hs.psegmenter == nil {
			C.free(unsafe.Pointer(strvalue))
			return -1, errors.New("Allocate C segmenter failed")
		}
		C.free(unsafe.Pointer(strvalue))
	}
	hs.uptunnel = tunnel
	if tunnel != nil {
		hs.busetunnel = true
		hs.localtuneladdr = tunnel.GetLocalTcpAddr()
	}
	hs.setduration = conf.duration
	hs.freg_window = conf.window
	hs.freg_extwindow = conf.extrwindow

	hs.ingest_urls = conf.ingesturls
	hs.upload_urls = conf.uploadinfos
	hs.buflist = list.New()
	hs.playbaseurl = baseurl
	hs.uploadbasepath = uploadbase

	baseurl = replaceurl(baseurl, uploadbase)
	if len(baseurl) <= 3 {
		return -2, errors.Errorf("playurl:%s uploadbase:%s replace failed", conf.channelurl, uploadbase)
	}
	lastindex := strings.LastIndex(baseurl, "/")
	if lastindex <= 0 {
		return -1, errors.New("post url invalid")
	}
	hs.baseurl = baseurl
	hs.basepath = baseurl[:lastindex+1]

	for _, url := range hs.upload_urls {
		newup := new(uploaders)
		newup.basepathurl = hs.basepath
		newup.buflist = list.New()
		newup.dellist = list.New()
		newup.sendlist = list.New()
		newup.remoteaddrs = url.addrs
		newup.upinfo = url
		if tunnel != nil && len(url.addrs) > 0 {
			newup.remote = url.addrs[0]
		} else {
			newup.remote = url.remote
		}
		newup.orgremote = url.remote
		newup.uploadurl = hs.baseurl + ".m3u8"
		newup.die = make(chan struct{})
		hs.seguploader = append(hs.seguploader, newup)
		go hs.uploadertask(newup)
	}
	go hs.looptask()
	return 0, nil
}
