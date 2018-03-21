package main

import (
	"log"
	"math/rand"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"net/http"
	_ "net/http/pprof"

	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

var (
	// VERSION is injected by buildflags
	VERSION = "SELFBUILD"
	// SALT is use for pbkdf2 key expansion
	SALT = "kcp-go"
)

func profilefunc(bindaddr string) {
	http.ListenAndServe(bindaddr, nil)
}

func gcMonitor() {
	timer := time.NewTicker(60 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			runtime.GC()
		}
	}
}

func checkUserNameValid(name string) bool {
	if len(name) <= 3 {
		return false
	}
	for idx := 0; idx < len(name); idx++ {
		chr := name[idx]
		if (chr >= 'a' && chr <= 'z') || (chr >= 'A' && chr <= 'Z') ||
			(chr >= '0' && chr <= '9') || chr == '_' {
			continue
		}
		return false
	}
	return true
}

func checknameValid(name string) error {
	if strings.Contains(name, "_url") || strings.Contains(name, "res_") || strings.Contains(name, "_nodes") ||
		strings.Contains(name, "_ingest") || strings.Contains(name, "_edge_") || strings.Contains(name, "_edges") ||
		strings.Contains(name, "_trans_") || strings.Contains(name, "_onlines") || strings.Contains(name, "_channels") {
		return errors.New("channelname cannot contain any of these:\"api\" \"url\" \"nodes\" \"_ingest\" \"_edge_\" \"_onlines\" \"_channels\"")
	}
	if !checkUserNameValid(name) {
		return errors.New("channel name invalid")
	}
	return nil
}

func checkchannelurl(url string) error {
	ncount := strings.Count(url, "/")
	first := strings.Index(url, "/")
	last := strings.LastIndex(url, "/")
	if ncount < 4 || first != 0 || last >= len(url)-2 {
		return errors.Errorf("channelurl invalid ,must be /live/[app]/[channelname]/[playbase]")
	}
	if strings.Contains(url, "nodes") || strings.Contains(url, "_ingest") ||
		strings.Contains(url, "_edge_") || strings.Contains(url, "trans") {
		return errors.New("channelurl cannot contain any of these:\"api\" \"url\" \"nodes\" \"_ingest\" \"_edge_\"")
	}
	if !checkUserNameValid(url[last+1:]) {
		return errors.New("channelurl playbase invalid")
	}
	return nil
}

func main() {
	// add more log flags for debugging
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	myApp := cli.NewApp()
	myApp.Name = "hlsuploader"
	myApp.Usage = "server(with SMUX)"
	myApp.Version = VERSION
	myApp.Flags = []cli.Flag{
		cli.StringFlag{
			Name:  "configure,c",
			Value: "",
			Usage: "config from json file",
		},
	}
	myApp.Action = func(c *cli.Context) error {
		startm := time.Now()
		rand.Seed(int64(time.Now().Nanosecond()))
		config := Config{}
		path := ""
		if c.String("c") == "" {
			path = "config.json"
		} else {
			path = c.String("c")
		}

		//Now only support json config file
		err := parseJSONConfig(&config, path)
		if err != nil {
			log.Printf("%v", err)
			return err
		}
		if checknameValid(config.NodeName) != nil {
			log.Printf("node name invalid:%v", err)
			return err
		}

		pcontrl := new(hlscontrol)
		ret, err := pcontrl.InitHlsControl(&config)
		if ret < 0 || err != nil {
			return err
		}
		log.Printf("Start service success,usetime:%v pid:%d", time.Since(startm), os.Getpid())
		if config.Pro.Bopen {
			go profilefunc(config.Pro.BindAdr)
		}
		go gcMonitor()
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		return nil
	}
	myApp.Run(os.Args)
}
