package main

import (
	"encoding/base64"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	tunnel "github.com/SkyGlance/kcp-tunnel"
	"github.com/SkyGlance/myhttp"
	jsoniter "github.com/json-iterator/go"
	"github.com/pkg/errors"
)

type accessctrl struct {
	startm     time.Time
	accesskey  string
	serveraddr string
	pctrl      *hlscontrol
	connectok  bool
}

type AcConnect struct {
	Cmd             string       `json:"cmd"`
	NodeName        string       `json:"node_name"`
	ListenAddr      string       `json:"listen_addr"`
	Serverbandwidth uint64       `json:"server_bandwidth"`
	Tunnel          TunnelConfig `json:"tunnel_config"`
	Uptime          string       `json:"up_time"`
	Chinfos         []ChanInfo   `json:"channel_infos"`
}

type AcCommand struct {
	Cmd      string           `json:"cmd"`
	Nstatus  int              `json:"nstatus,omitempty"`
	Err      string           `json:"error,omitempty"`
	Seqid    uint32           `json:"sequece_id,omitempty"`
	Channels []UpLoadChannels `json:"upload_channels,omitempty"`
}

type AcCommandReply struct {
	Status string `json:"status"`
	Err    string `json:"error"`
	Seqid  uint32 `json:"sequece_id"`
}

// /api/ingest_control_cmd&key=axlkjhvalksqwoiuaddfvc

func (as *accessctrl) runWrite(mc *myhttp.MyHttpClient,
	writechan chan *myhttp.MyHttpData, rddie chan struct{}, wrdie chan struct{}) {
	defer close(wrdie)
	checktimer := time.NewTicker(3 * time.Second)
	lastchecktm := time.Now()
	for {
		select {
		case <-checktimer.C:
			lastduration := time.Since(lastchecktm)
			if lastduration >= 30*time.Second {
				log.Printf("Access ctrl server:%s write timeout", as.serveraddr)
				return
			}
		case data := <-writechan:
			n, err := mc.SendChunkedData(data.Data, 5*time.Second)
			if err != nil || n <= 0 {
				log.Printf("Access ctrl server:%s write failed", as.serveraddr)
				return
			}
			lastchecktm = time.Now()
		case <-rddie:
			return
		}
	}
}

func (as *accessctrl) runRead(mc *myhttp.MyHttpClient,
	readchan chan *myhttp.MyHttpData, rddie chan struct{}) {
	defer close(rddie)
	for {
		data := make([]byte, 8192)
		n, err, data := mc.ReadChunkData(data, 30*time.Second)
		if n <= 0 || err != nil {
			log.Printf("Access ctrl server:%s read failed", as.serveraddr)
			return
		}
		pdata := new(myhttp.MyHttpData)
		pdata.Chunked = true
		pdata.Data = data[:n]
		readchan <- pdata
	}
}

func (as *accessctrl) runInternal() {
	mc := myhttp.NewHttpClient(as.serveraddr, time.Second*10)
	if mc == nil {
		log.Printf("Connect resource ctrl server:%s failed", as.serveraddr)
		return
	}
	defer mc.Close()
	mc.SetSendHeader("Host", as.serveraddr)
	mc.SetSendHeader("Content-Type", "application/json")
	mc.SetSendHeader("User-Agent", "hls_ingester")
	mc.SetSendHeader("Cache-Control", "no-cache")
	mc.SetSendHeader("Transfer-Encoding", "chunked")
	mc.SetSendHeader("Connection", "close")
	constr := fmt.Sprintf("/api/ingest_control_cmd&key=%s", as.accesskey)
	err := mc.SendRequestHeader("GET", constr, time.Second*10)
	if err != nil {
		log.Printf("Send request to resource ctrl server:%s failed", as.serveraddr)
		return
	}
	header := mc.GetHeader()
	if header == nil {
		log.Printf("Get request header from resource ctrl server:%s failed", as.serveraddr)
		return
	}
	statuscode, ok := (*header)["StatusCode"]
	if !ok {
		log.Printf("Header form resource ctrl server missmatch", as.serveraddr)
		return
	}
	nstatus, err := strconv.Atoi(statuscode)
	if err != nil {
		log.Printf("resource ctrl server:%s no status reply", as.serveraddr)
		return
	}
	if nstatus != 200 {
		log.Printf("resource ctrl server:%s reply status:%d", as.serveraddr, nstatus)
		return
	}
	log.Printf("Connect resource ctrl server:%s success", as.serveraddr)
	readchan := make(chan *myhttp.MyHttpData, 5)
	rddie := make(chan struct{})
	go as.runRead(mc, readchan, rddie)

	writechan := make(chan *myhttp.MyHttpData, 5)
	wrdie := make(chan struct{})
	go as.runWrite(mc, writechan, rddie, wrdie)

	timer := time.NewTicker(5000 * time.Millisecond)
	defer timer.Stop()
	//write connection cmd
	connect := AcConnect{
		Cmd:             "sync",
		NodeName:        as.pctrl.config.NodeName,
		ListenAddr:      as.pctrl.config.ListenAddr,
		Serverbandwidth: as.pctrl.config.Serverbandwidth,
		Tunnel:          as.pctrl.config.Tunnel,
		Uptime:          fmt.Sprintf("%v", time.Since(as.startm)),
		Chinfos:         as.pctrl.collectchannelinfos(),
	}
	cvalue, err := jsoniter.Marshal(connect)
	if err != nil {
		log.Printf("connect resource ctrl server cmd error:%v", err)
		return
	}
	writedata := new(myhttp.MyHttpData)
	writedata.Chunked = true
	writedata.Offset = 0
	writedata.Data = cvalue
	writechan <- writedata

	lastreadtm := time.Now()
	for {
		select {
		case read := <-readchan:
			if !read.Chunked {
				log.Printf("Resource ctrl server:%s encoding is not chunked", as.serveraddr)
				return
			}
			checkcmd := AcCommand{}
			err = jsoniter.Unmarshal(read.Data, &checkcmd)
			if err != nil {
				log.Printf("Resource ctrl server:%s encoding:%s error", as.serveraddr, string(read.Data))
				return
			}
			lastreadtm = time.Now()
			if checkcmd.Cmd != "sync_channels" {
				log.Printf("Resource ctrl server:%s encoding:%s cmd:%s error",
					as.serveraddr, string(read.Data), checkcmd.Cmd)
				return
			}

			//check reply
			if checkcmd.Nstatus != 200 {
				log.Printf("Connect Resource ctrl server:%s failed,err:%s",
					as.serveraddr, checkcmd.Err)
				return
			}
			//if ok
			as.connectok = true
			if len(checkcmd.Channels) <= 0 {
				break
			}
			var reply AcCommandReply
			err = as.pctrl.sync_channels(checkcmd.Channels)
			if err != nil {
				reply = AcCommandReply{
					Status: "failed",
					Seqid:  checkcmd.Seqid,
					Err:    err.Error(),
				}
			} else {
				reply = AcCommandReply{
					Status: "success",
					Seqid:  checkcmd.Seqid,
					Err:    "",
				}
			}
			redata, err := jsoniter.Marshal(reply)
			if err != nil {
				log.Printf("create json failed:%v", err)
				return
			}
			if len(writechan) >= cap(writechan) {
				break
			}
			replydata := new(myhttp.MyHttpData)
			replydata.Chunked = true
			replydata.Offset = 0
			replydata.Data = redata
			writechan <- replydata

		case <-timer.C:
			last := time.Since(lastreadtm)
			if last >= 30*time.Second {
				log.Printf("Resource ctrl server:%s sync timeout", as.serveraddr)
				return
			}
			if len(writechan) >= cap(writechan) {
				log.Printf("Resource ctrl server:%s write chan blocking", as.serveraddr)
				return
			}
			//sync cmd
			connect := AcConnect{
				Cmd:             "sync",
				NodeName:        as.pctrl.config.NodeName,
				ListenAddr:      as.pctrl.config.ListenAddr,
				Serverbandwidth: as.pctrl.config.Serverbandwidth,
				Tunnel:          as.pctrl.config.Tunnel,
				Uptime:          fmt.Sprintf("%v", time.Since(as.startm)),
				Chinfos:         as.pctrl.collectchannelinfos(),
			}
			cvalue, err := jsoniter.Marshal(connect)
			if err != nil {
				log.Printf("connect resource ctrl server cmd error:%v", err)
				return
			}
			writedata := new(myhttp.MyHttpData)
			writedata.Chunked = true
			writedata.Offset = 0
			writedata.Data = cvalue
			writechan <- writedata
		case <-rddie:
			return
		case <-wrdie:
			return
		}
	}
}

func (as *accessctrl) runctrl() {
	as.startm = time.Now()
	for {
		as.connectok = false
		as.runInternal()
		as.connectok = false
		time.Sleep(time.Second * 5)
	}
}

func (as *accessctrl) InitAccessCtrl(pctrl *hlscontrol, seraddr string, akey string) int {
	as.accesskey = base64.URLEncoding.EncodeToString([]byte(akey))
	as.serveraddr = seraddr
	as.pctrl = pctrl
	go as.runctrl()
	return 0
}

type hlscontrol struct {
	uptunnel       *tunnel.UpServTunneler
	udpbindaddress []string

	config       *Config
	cmds         *httpcmd
	localbindstr string
	uploadbase   string
	server       *httpserver
	actrl        *accessctrl

	upchannelmaps map[string]*upServerTunnelConfig
	mapmux        sync.Mutex
	hlssegs       map[string]*HlsSegmenter
}

func (hc *hlscontrol) collectchannelinfos() (chinfos []ChanInfo) {
	hc.mapmux.Lock()
	hc.mapmux.Unlock()
	for _, pseg := range hc.hlssegs {
		chinfos = append(chinfos, pseg.GetChannelInfo())
	}
	return
}

func (hc *hlscontrol) sync_channels(upchannels []UpLoadChannels) error {
	hc.mapmux.Lock()
	hc.mapmux.Unlock()
	if len(upchannels) <= 0 {
		log.Printf("Delete all trans channels")
		hc.upchannelmaps = make(map[string]*upServerTunnelConfig)
		for _, pseg := range hc.hlssegs {
			go pseg.CloseSegmenter()
		}
		hc.hlssegs = make(map[string]*HlsSegmenter)
		return nil
	}
	for segurl, pseg := range hc.hlssegs {
		var bfind bool
		for _, channel := range upchannels {
			if channel.ChannelUrl == pseg.conf.channelurl {
				bfind = true
				break
			}
		}
		if !bfind {
			go pseg.CloseSegmenter()
			delete(hc.hlssegs, segurl)
		}
	}
	for _, channel := range upchannels {
		if len(channel.ChannelUrl) <= 3 {
			continue
		}
		if len(channel.IngestUrls) <= 0 || len(channel.UploadServers) <= 0 {
			continue
		}
		channelconf, find := hc.upchannelmaps[channel.ChannelUrl]
		if !find {
			delseg, find := hc.hlssegs[channel.ChannelUrl]
			if find {
				go delseg.CloseSegmenter()
				delete(hc.hlssegs, channel.ChannelUrl)
			}
			channelconf = new(upServerTunnelConfig)
			channelconf.channelname = channel.ChannelName
			channelconf.channelurl = channel.ChannelUrl
			channelconf.duration = channel.FregDuration
			if channelconf.duration <= 500 {
				channelconf.duration = 500
			}
			channelconf.window = channel.FregWindow
			if channelconf.window <= 2 {
				channelconf.window = 2
			}
			channelconf.extrwindow = channel.FregExtraWindow
			if channelconf.extrwindow <= 1 {
				channelconf.extrwindow = 1
			}
			channelconf.ingesturls = channel.IngestUrls

			for _, upurls := range channel.UploadServers {
				ok, ip, port, startport, endport := getuploadstring(upurls.Address)
				if !ok || len(ip) <= 3 {
					log.Printf("Del channel:%s url:%s invalid upload address:%s", channelconf.channelname, channelconf.channelurl, upurls.Address)
					return errors.Errorf("channel:%s edgeserver:%s address:%s invalid",
						channelconf.channelurl, upurls.NodeName, upurls.Address)
				}
				upinfo := uploadinfo{}
				upinfo.nodename = upurls.NodeName
				upinfo.ip = ip
				upinfo.remote = fmt.Sprintf("%s:%d", ip, port)
				upinfo.tunnelstartport = startport
				upinfo.tunnelendport = endport
				for i := 0; i < (endport - startport); i++ {
					upinfo.addrs = append(upinfo.addrs, fmt.Sprintf("%s:%d", ip, startport+i))
				}
				channelconf.uploadinfos = append(channelconf.uploadinfos, upinfo)
			}
			newseg := new(HlsSegmenter)
			ret, err := newseg.StartSegmenter(hc.uptunnel, channelconf, channel.ChannelName, hc.uploadbase)
			if ret < 0 || err != nil {
				return errors.Errorf("channel:%s start segmenter failed", channelconf.channelurl)
			}
			hc.hlssegs[channel.ChannelUrl] = newseg
			hc.upchannelmaps[channel.ChannelUrl] = channelconf
		} else {
			oldseg, find := hc.hlssegs[channel.ChannelUrl]
			if !find {
				log.Printf("internal error detected,map value empty!")
				delete(hc.upchannelmaps, channel.ChannelUrl)
				continue
			}
			if channel.FregDuration != channelconf.duration {
				oldseg.UpdateSegDuration(channel.FregDuration)
				channelconf.duration = channel.FregDuration
			}
			if channel.FregWindow != channelconf.window ||
				channel.FregExtraWindow != channelconf.extrwindow {
				oldseg.UpdateWindow(channel.FregWindow, channel.FregExtraWindow)
				channelconf.extrwindow = channel.FregExtraWindow
				channelconf.window = channel.FregWindow
			}
			var bchanged bool
			if len(channel.IngestUrls) != len(channelconf.ingesturls) {
				bchanged = true
			} else {
				for _, newurl := range channel.IngestUrls {
					var bfind bool
					for _, url := range channelconf.ingesturls {
						if newurl == url {
							bfind = true
							break
						}
					}
					if !bfind {
						bchanged = true
						break
					}
				}
			}
			if bchanged {
				oldseg.UpdateIngestUrls(channel.IngestUrls)
				channelconf.ingesturls = channel.IngestUrls
			}
			var uploadinfos []uploadinfo
			for _, upurls := range channel.UploadServers {
				ok, ip, port, startport, endport := getuploadstring(upurls.Address)
				if !ok || len(ip) <= 3 {
					log.Printf("Del channel:%s url:%s invalid upload address:%s", channelconf.channelname, channelconf.channelurl, upurls.Address)
					return errors.Errorf("channel:%s edgeserver:%s address:%s invalid",
						channelconf.channelurl, upurls.NodeName, upurls.Address)
				}
				upinfo := uploadinfo{}
				upinfo.nodename = upurls.NodeName
				upinfo.ip = ip
				upinfo.remote = fmt.Sprintf("%s:%d", ip, port)
				upinfo.tunnelstartport = startport
				upinfo.tunnelendport = endport
				for i := 0; i < (endport - startport); i++ {
					upinfo.addrs = append(upinfo.addrs, fmt.Sprintf("%s:%d", ip, startport+i))
				}
				uploadinfos = append(uploadinfos, upinfo)
			}
			channelconf.uploadinfos = uploadinfos
			oldseg.UpdatePostServers(uploadinfos)
		}
	}

	return nil
}

func (hc *hlscontrol) addchannelupload(url string, uploadurls []UpServer) (int, error) {
	hc.mapmux.Lock()
	hc.mapmux.Unlock()
	mapchannel, find := hc.upchannelmaps[url]
	if !find {
		return -1, errors.Errorf("channel:%s not exist", url)
	}
	upload, find := hc.hlssegs[url]
	if !find {
		for _, upurls := range uploadurls {
			ok, ip, port, startport, endport := getuploadstring(upurls.Address)
			if !ok || len(ip) <= 3 {
				log.Printf("Del channel:%s url:%s invalid upload address:%s", mapchannel.channelname, mapchannel.channelurl, upurls.Address)
				return -2, errors.Errorf("channel:%s edgeserver:%s address:%s invalid",
					url, upurls.NodeName, upurls.Address)
			}
			upinfo := uploadinfo{}
			upinfo.nodename = upurls.NodeName
			upinfo.ip = ip
			upinfo.remote = fmt.Sprintf("%s:%d", ip, port)
			upinfo.tunnelstartport = startport
			upinfo.tunnelendport = endport
			for i := 0; i < (endport - startport); i++ {
				upinfo.addrs = append(upinfo.addrs, fmt.Sprintf("%s:%d", ip, startport+i))
			}
			mapchannel.uploadinfos = append(mapchannel.uploadinfos, upinfo)
		}
		if len(mapchannel.uploadinfos) <= 0 || len(mapchannel.ingesturls) <= 0 {
			emptyvalue := ""
			if len(mapchannel.uploadinfos) <= 0 {
				emptyvalue = "edgeservers"
			} else {
				emptyvalue = "ingestsource"
			}
			return -2, errors.Errorf("channel:%s %s empty", url, emptyvalue)
		}
		newseg := new(HlsSegmenter)
		ret, err := newseg.StartSegmenter(hc.uptunnel, mapchannel, mapchannel.channelname, hc.uploadbase)
		if ret < 0 || err != nil {
			return -2, errors.Errorf("channel:%s start segmenter failed", url)
		}
		hc.hlssegs[url] = newseg
		return 0, nil
	}
	for _, upurls := range uploadurls {
		ok, ip, port, startport, endport := getuploadstring(upurls.Address)
		if !ok || len(ip) <= 3 {
			log.Printf("Add channel:%s url:%s invalid upload address:%s",
				mapchannel.channelname, mapchannel.channelurl, upurls.Address)
			return -2, errors.Errorf("channel:%s edgeserver:%s address:%s invalid",
				url, upurls.NodeName, upurls.Address)
		}
		var bfind bool
		var infos []uploadinfo
		for _, oldurls := range mapchannel.uploadinfos {
			if oldurls.ip == ip {
				bfind = true
				oldurls.remote = fmt.Sprintf("%s:%d", ip, port)
				var addrs []string
				for i := 0; i < (endport - startport); i++ {
					addrs = append(addrs, fmt.Sprintf("%s:%d", ip, startport+i))
				}
				oldurls.addrs = addrs
				break
			}
			if oldurls.nodename == upurls.NodeName {
				return -3, errors.Errorf("url:%s Add channel:%s address:%s nodename:%s already in use",
					url, mapchannel.channelname, upurls.Address, upurls.NodeName)
			}
			infos = append(infos, oldurls)
		}
		if bfind {
			upload.UpdatePostServers(infos)
			mapchannel.uploadinfos = infos
			continue
		}
		var info uploadinfo
		info.ip = ip
		info.remote = fmt.Sprintf("%s:%d", ip, port)
		info.nodename = upurls.NodeName
		info.tunnelstartport = startport
		info.tunnelendport = endport
		for i := 0; i < (endport - startport); i++ {
			info.addrs = append(info.addrs, fmt.Sprintf("%s:%d", ip, startport+i))
		}
		mapchannel.uploadinfos = append(mapchannel.uploadinfos, info)
	}
	upload.UpdatePostServers(mapchannel.uploadinfos)
	return 0, nil
}

func (hc *hlscontrol) delchannelupload(url string, uploadurls []UpServer) (int, error) {
	hc.mapmux.Lock()
	hc.mapmux.Unlock()
	mapchannel, find := hc.upchannelmaps[url]
	if !find {
		return -1, errors.Errorf("channel url:%s empty", url)
	}
	upload, find := hc.hlssegs[url]
	if !find {
		return -2, errors.Errorf("channel url:%s empty", url)
	}
	var uploadinfos []uploadinfo
	for _, upurls := range uploadurls {
		ok, ip, _, _, _ := getuploadstring(upurls.Address)
		if !ok || len(ip) <= 3 {
			return -3, errors.Errorf("url:%s channel:%s invalid upload address:%s",
				url, mapchannel.channelname, upurls.Address)
		}
		for _, olup := range mapchannel.uploadinfos {
			if olup.ip == ip || olup.nodename == upurls.NodeName {
				continue
			}
			uploadinfos = append(uploadinfos, olup)
		}
	}
	if len(uploadinfos) <= 0 {
		hc.delchannel(url)
		return 0, nil
	}
	mapchannel.uploadinfos = uploadinfos
	if upload.UpdatePostServers(uploadinfos) < 0 {
		return -3, errors.Errorf("url:%s update hls ingester faild", url)
	}
	return 0, nil
}

func (hc *hlscontrol) addchannelingest(url string, ingesturls []string) (int, error) {
	hc.mapmux.Lock()
	hc.mapmux.Unlock()
	if len(ingesturls) <= 3 {
		return -1, errors.Errorf("channel url:%s empty", url)
	}
	mapchannel, find := hc.upchannelmaps[url]
	if !find {
		return -2, errors.Errorf("channel url:%s not exsit", url)
	}
	for _, newurl := range ingesturls {
		var bfind bool
		for _, olurl := range mapchannel.ingesturls {
			if newurl == olurl {
				bfind = true
				break
			}
		}
		if !bfind {
			mapchannel.ingesturls = append(mapchannel.ingesturls)
		}
	}
	upload, find := hc.hlssegs[url]
	if !find {
		if len(mapchannel.uploadinfos) <= 0 || len(mapchannel.ingesturls) <= 0 {
			return 0, nil
		}
		newseg := new(HlsSegmenter)
		ret, err := newseg.StartSegmenter(hc.uptunnel, mapchannel, mapchannel.channelname, hc.uploadbase)
		if ret < 0 || err != nil {
			return 0, errors.Errorf("url:%s start hls ingester faild", url)
		}
		hc.hlssegs[url] = newseg
		return 0, nil
	}
	if upload.UpdateIngestUrls(mapchannel.ingesturls) < 0 {
		return -3, errors.Errorf("url:%s update hls ingester faild", url)
	}
	return 0, nil
}

func (hc *hlscontrol) delchannelingest(url string, ingesturls []string) (int, error) {
	hc.mapmux.Lock()
	hc.mapmux.Unlock()
	if len(ingesturls) <= 3 {
		return -1, errors.Errorf("channel url:%s empty", url)
	}
	mapchannel, find := hc.upchannelmaps[url]
	if !find {
		return -2, errors.Errorf("channel url:%s not exsit", url)
	}
	var urls []string
	for _, olurl := range mapchannel.ingesturls {
		var bfind bool
		for _, newurl := range ingesturls {
			if olurl == newurl {
				bfind = true
				break
			}
		}
		if !bfind {
			urls = append(urls, olurl)
		}
	}
	mapchannel.ingesturls = urls
	if len(urls) <= 0 {
		upload, find := hc.hlssegs[url]
		if find {
			go upload.CloseSegmenter()
			delete(hc.hlssegs, url)
		}
		return 0, nil
	}
	upload, find := hc.hlssegs[url]
	if find {
		if upload.UpdateIngestUrls(urls) < 0 {
			return -3, errors.Errorf("url:%s update hls ingester faild", url)
		}
	}
	return 0, nil
}

func (hc *hlscontrol) addchannel(name, url string, duration, window, extrawindow int) int {
	hc.mapmux.Lock()
	hc.mapmux.Unlock()
	mapchannel, find := hc.upchannelmaps[url]
	if !find {
		newup := new(upServerTunnelConfig)
		newup.channelname = name
		newup.channelurl = url
		newup.duration = duration
		newup.window = window
		newup.extrwindow = extrawindow
		return 0
	} else {
		mapchannel.channelname = name
		mapchannel.channelurl = url
		mapchannel.duration = duration
		mapchannel.window = window
		mapchannel.extrwindow = extrawindow
	}
	upload, find := hc.hlssegs[url]
	if !find {
		return 0
	}
	upload.UpdateSegDuration(duration)
	upload.UpdateWindow(window, extrawindow)
	return 0
}

func (hc *hlscontrol) updatechannel(name, url string, duration, nwindow, extrawindow int) (int, error) {
	hc.mapmux.Lock()
	hc.mapmux.Unlock()
	_, find := hc.upchannelmaps[url]
	if !find {
		return -1, errors.Errorf("channel url:%s empty", url)
	}
	upload, find := hc.hlssegs[url]
	if !find {
		return -2, errors.Errorf("channel url:%s empty", url)
	}
	if nwindow <= 2 {
		nwindow = 2
	}
	if extrawindow <= 1 {
		extrawindow = 1
	}
	if duration <= 500 {
		duration = 500
	}

	upload.UpdateWindow(nwindow, extrawindow)
	if upload.UpdateSegDuration(duration) < 0 {
		return -3, errors.Errorf("channel url:%s update segmenter duration failed")
	}
	return 0, nil
}

func (hc *hlscontrol) delchannel(url string) (int, error) {
	hc.mapmux.Lock()
	hc.mapmux.Unlock()
	closeg, find := hc.hlssegs[url]
	if !find {
		return -1, errors.Errorf("channel url:%s not exist", url)
	}
	go closeg.CloseSegmenter()
	delete(hc.hlssegs, url)
	delete(hc.upchannelmaps, url)
	return 0, nil
}

func (hc *hlscontrol) GetAllChannelInfo() string {

	return ""
}

func (hc *hlscontrol) GetChannelInfo(url string) string {

	return ""
}

type Controlcmd struct {
	Cmd             string     `json:"cmd"`
	ChannelName     string     `json:"channel_name,omitempty"`
	ChannelUrl      string     `json:"channel_url"`
	FregDuration    int        `json:"fregment_duration,omitempty"`
	FregWindow      int        `json:"fregment_window,omitempty"`
	FregExtraWindow int        `json:"fregment_extra_window,omitempty"`
	IngestUrls      []string   `json:"ingest_urls,omitempty"`
	UploadServers   []UpServer `json:"upload_servers,omitempty"`
}

func (hc *hlscontrol) ChannelAPIControl(data []byte) string {
	cmd := Controlcmd{}
	err := jsoniter.Unmarshal(data, &cmd)
	if err != nil {
		return "{\"status\":\"failed\",\"error\":\"unrecognized format\"}"
	}
	if cmd.Cmd == "add_channel" {
		if hc.addchannel(cmd.ChannelName, cmd.ChannelUrl,
			cmd.FregDuration, cmd.FregWindow, cmd.FregExtraWindow) < 0 {
			return "{\"status\":\"failed\",\"error\":\"add channel failed\"}"
		}
		_, err := hc.addchannelingest(cmd.ChannelUrl, cmd.IngestUrls)
		if err != nil {
			return "{\"status\":\"failed\",\"error\":\"add channel ingesters failed\"}"
		}
		_, err = hc.addchannelupload(cmd.ChannelUrl, cmd.UploadServers)
		if err != nil {
			return "{\"status\":\"failed\",\"error\":\"add channel upload failed\"}"
		}
		return "{\"status\":\"success\",\"error\":\"ok\"}"
	} else if cmd.Cmd == "del_channel" {
		_, err := hc.delchannel(cmd.ChannelUrl)
		if err != nil {
			return "{\"status\":\"failed\",\"error\":\"channel didnot exist\"}"
		}
		return "{\"status\":\"success\",\"error\":\"ok\"}"
	} else {
		return "{\"status\":\"failed\",\"error\":\"unrecognized command\"}"
	}
}

func getuploadstring(url string) (bool, string, int, int, int) {
	portrangeindex := strings.Index(url, "/")
	if portrangeindex <= 0 {
		portindex := strings.Index(url, ":")
		if portindex <= 0 {
			return true, url, 80, 0, 0
		}
		upport, err := strconv.ParseInt(url[portindex+1:], 10, 32)
		if err != nil || upport > 655535 || upport <= 10 {
			return false, "", 0, 0, 0
		}
		return true, url[:portindex], int(upport), 0, 0
	}
	startport, endport := 0, 0
	n, err := fmt.Sscanf(url[portrangeindex+1:], "%d:%d", &startport, &endport)
	if err != nil || n != 2 || endport <= startport || endport > 65535 || startport <= 10 {
		return false, "", 0, 0, 0
	}
	portindex := strings.Index(url[:portrangeindex], ":")
	if portindex <= 0 {
		return true, url[:portrangeindex], 80, startport, endport
	}
	upport, err := strconv.ParseInt(url[portindex+1:portrangeindex], 10, 32)
	if err != nil || upport > 655535 || upport <= 10 {
		return false, "", 0, 0, 0
	}
	return true, url[:portindex], int(upport), startport, endport
}

func (hc *hlscontrol) InitHlsControl(config *Config) (int, error) {
	if config == nil {
		return -1, errors.New("pramter empty")
	}
	if len(config.NodeName) <= 2 {
		return -1, errors.New("configure \"node_name\" empty")
	}
	hc.hlssegs = make(map[string]*HlsSegmenter)
	hc.upchannelmaps = make(map[string]*upServerTunnelConfig)

	hc.cmds = new(httpcmd)
	hc.cmds.initAdminCmds(hc)
	hc.server = new(httpserver)
	hc.uploadbase = config.UploadBase
	if hc.uploadbase[0] != '/' {
		return -1, errors.New("configure \"upload_url_base\" url invalid")
	}
	if hc.server.InitHttpServer(hc.cmds, config.ListenAddr) < 0 {
		return -2, errors.Errorf("bind addrress:%s failed", config.ListenAddr)
	}
	if config.Control.Allow {
		hc.actrl = new(accessctrl)
		if hc.actrl.InitAccessCtrl(hc, config.Control.ServerAddr, config.Control.AccessKey) < 0 {
			return -2, errors.New("Start access control failed")
		}
	}
	hc.uptunnel = nil
	if strings.ToUpper(config.Tunnel.TunOn) == "ON" {
		if config.Tunnel.Maxbandwidth < config.Tunnel.Minbandwidth {
			return -2, errors.New("Config tunnel bandwidth error")
		}
		hc.localbindstr = fmt.Sprintf("127.0.0.1:%d", config.Tunnel.InternalPort)
		var portstart, portend int = 0, 0
		n, err := fmt.Sscanf(config.Tunnel.Portrange, "%d:%d", &portstart, &portend)
		if err != nil || n != 2 || portstart >= portend || portend > 65535 || portstart <= 100 {
			return -2, errors.New("Tunnel port-range error, (100-65535)")
		}
		for i := 0; i < (portend - portstart); i++ {
			hc.udpbindaddress = append(hc.udpbindaddress, fmt.Sprintf(":%d", portstart+i))
		}
		config.Serverbandwidth = config.Serverbandwidth * 1000 / 8
		config.Tunnel.Maxbandwidth = config.Tunnel.Maxbandwidth * 1000 / 8
		config.Tunnel.Minbandwidth = config.Tunnel.Minbandwidth * 1000 / 8
		hc.uptunnel, err = tunnel.CreateUpServTunnel(config.Tunnel.Minbandwidth,
			config.Tunnel.Maxbandwidth, config.Serverbandwidth, hc.udpbindaddress, hc.localbindstr)
		if err != nil {
			return -3, err
		}
	}
	hc.mapmux.Lock()
	defer hc.mapmux.Unlock()
	//
	for _, channel := range config.Channels {
		if checkchannelurl(channel.ChannelUrl) != nil || checknameValid(channel.ChannelName) != nil {
			log.Printf("channel:%s url:%s invalid ignore", channel.ChannelName, channel.ChannelUrl)
			continue
		}
		_, ok := hc.upchannelmaps[channel.ChannelUrl]
		if ok {
			return -4, errors.Errorf("Channel_url:%s already being use", channel.ChannelUrl)
		}
		if len(channel.UploadServers) <= 0 || len(channel.IngestUrls) <= 0 {
			log.Printf("channel:%s url:%s ingest/upload empty ignore", channel.ChannelName, channel.ChannelUrl)
			continue
		}
		uptunnel := new(upServerTunnelConfig)
		uptunnel.channelname = channel.ChannelName
		uptunnel.channelurl = channel.ChannelUrl
		uptunnel.duration = channel.FregDuration
		uptunnel.window = channel.FregWindow
		uptunnel.extrwindow = channel.FregExtraWindow
		uptunnel.ingesturls = channel.IngestUrls
		for _, upload := range channel.UploadServers {
			ok, ip, port, startport, endport := getuploadstring(upload.Address)
			if !ok || len(ip) <= 3 {
				log.Printf("channel:%s url:%s invalid upload address:%s", channel.ChannelName, channel.ChannelUrl, upload.Address)
				continue
			}
			upinfo := uploadinfo{}
			upinfo.nodename = upload.NodeName
			upinfo.ip = ip
			upinfo.remote = fmt.Sprintf("%s:%d", ip, port)
			upinfo.tunnelstartport = startport
			upinfo.tunnelendport = endport
			for i := 0; i < (endport - startport); i++ {
				upinfo.addrs = append(upinfo.addrs, fmt.Sprintf("%s:%d", ip, startport+i))
			}
			uptunnel.uploadinfos = append(uptunnel.uploadinfos, upinfo)
		}
		newseg := new(HlsSegmenter)
		ret, err := newseg.StartSegmenter(hc.uptunnel, uptunnel, channel.ChannelName, hc.uploadbase)
		if ret < 0 || err != nil {
			return -4, errors.Errorf("channel:%s url:%s start segmenter failed", channel.ChannelName, channel.ChannelUrl)
		}
		hc.hlssegs[channel.ChannelUrl] = newseg
		hc.upchannelmaps[channel.ChannelUrl] = uptunnel
	}
	return 0, nil
}
