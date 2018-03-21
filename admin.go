package main

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/SkyGlance/myhttp"

	"github.com/pkg/errors"
)

var (
	admintimeout             = 30 * time.Second
	maxpostcontentlenallowed = 655350
)

type cmdHandler func(cmd *httpcmd, client *myhttp.MyHttpClient) (string, error)

type httpcmd struct {
	adminCommands map[string]cmdHandler
	control       *hlscontrol
}

func (cm *httpcmd) initAdminCmds(ctrl *hlscontrol) {
	cm.adminCommands = make(map[string]cmdHandler)
	cm.adminCommands["/api/query_allchannelinfo"] = _queryallchannelinfo
	cm.adminCommands["/api/query_channel_transinfo"] = _querychannelinfo
	cm.control = ctrl
}

func (cm *httpcmd) adminHandler(client *myhttp.MyHttpClient) {
	defer client.Close()
	outstr := ""
	header, err := client.RecvHeader(admintimeout / 2)
	if header == nil || err != nil {
		return
	}
	method, ok := header["Method"]
	if !ok {
		cm._replyHandler(client, 400, outstr)
		return
	}
	if method == "GET" {
		url, ok := header["Url"]
		if !ok {
			cm._replyHandler(client, 400, outstr)
			return
		}
		index := strings.Index(url, "&url=")
		if index > 0 {
			url = url[0:index]
		}
		mfunc, ok := cm.adminCommands[url]
		if !ok {
			cm._replyHandler(client, 501, outstr)
			return
		}
		outstr, err = mfunc(cm, client)
		if err != nil {
			return
		}
		cm._replyHandler(client, 200, outstr)
	} else if method == "POST" {
		url, ok := header["Url"]
		if !ok {
			cm._replyHandler(client, 400, outstr)
			return
		}
		if url != "/api/channel_api_control" {
			cm._replyHandler(client, 405, outstr)
			return
		}
		content, ok := header["Content-Length"]
		if !ok {
			cm._replyHandler(client, 411, outstr)
			return
		}
		contentlen, err := strconv.ParseInt(content, 10, 64)
		if err != nil || contentlen <= 0 {
			cm._replyHandler(client, 400, outstr)
			return
		}
		if contentlen > int64(maxpostcontentlenallowed) {
			cm._replyHandler(client, 413, outstr)
			return
		}
		outstr, err = cm._channelapicontrol(client, int(contentlen))
		if err != nil {
			return
		}
		cm._replyHandler(client, 200, outstr)
	} else {
		cm._replyHandler(client, 405, outstr)
	}
}

func (cm *httpcmd) _replyHandler(client *myhttp.MyHttpClient, nstatus int, value string) {
	if len(value) > 0 {
		client.SetSendHeader("Content-Type", "application/json")
		client.SetSendHeader("Content-Length", fmt.Sprintf("%d", len(value)))
	}
	client.SetSendHeader("Connection", "close")
	err := client.SendStatusHeader(nstatus, admintimeout/2)
	if err != nil {
		return
	}
	if len(value) > 0 {
		out := []byte(value)
		client.SendData(out, admintimeout)
	}
}

func _queryallchannelinfo(cm *httpcmd, client *myhttp.MyHttpClient) (string, error) {

	return cm.control.GetAllChannelInfo(), nil
}

func _querychannelinfo(cm *httpcmd, client *myhttp.MyHttpClient) (string, error) {
	header := client.GetHeader()
	url, _ := (*header)["Url"]
	index := strings.Index(url, "&url=")
	if index <= 0 || index+5 >= len(url) {
		cm._replyHandler(client, 406, "")
		return "", errors.New("Paramter empty")
	}
	url = url[index+5:]
	uDec, err := base64.URLEncoding.DecodeString(url)
	if err != nil {
		cm._replyHandler(client, 422, "")
		return "", err
	}
	url = string(uDec[0:])
	//get channel info url
	return cm.control.GetChannelInfo(url), nil
}

func (cm *httpcmd) _channelapicontrol(client *myhttp.MyHttpClient, contentlen int) (string, error) {

	readbuf := make([]byte, 0, contentlen)
	n, err := client.ReadDataFully(readbuf, admintimeout)
	if err != nil || n != contentlen {
		cm._replyHandler(client, 408, "")
		return "", errors.New("read failed")
	}
	return cm.control.ChannelAPIControl(readbuf), nil
}
