package main

import (
	"time"

	"github.com/SkyGlance/myhttp"
)

type httpserver struct {
	pserver *myhttp.MyHttpServer
	binaddr string
	cmd     *httpcmd
}

func (hs *httpserver) runhttpclient(client *myhttp.MyHttpClient) {
	hs.cmd.adminHandler(client)
}

func (hs *httpserver) runhttpserver() {
	for {
		client := hs.pserver.AcceptClient()
		if client == nil {
			time.Sleep(time.Millisecond * 500)
			continue
		}
		go hs.runhttpclient(client)
	}
}

func (hs *httpserver) InitHttpServer(cmd *httpcmd, binaddr string) int {
	hs.pserver = myhttp.NewHttpServer(binaddr)
	if hs.pserver == nil {
		return -1
	}
	hs.cmd = cmd
	hs.binaddr = binaddr
	go hs.runhttpserver()
	return 0
}
