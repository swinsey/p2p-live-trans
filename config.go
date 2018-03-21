package main

import (
	"encoding/json"
	"os"
)

type uploadinfo struct {
	nodename string
	remote   string

	ip              string
	tunnelstartport int
	tunnelendport   int
	addrs           []string
}

type upServerTunnelConfig struct {
	channelname string
	channelurl  string
	duration    int
	window      int
	extrwindow  int
	ingesturls  []string

	uploadinfos []uploadinfo
}

type TunnelConfig struct {
	TunOn        string `json:"module"`
	InternalPort int    `json:"internal_port"`
	Portrange    string `json:"port_range"`
	AccessKey    string `json:"access_key"`
	Maxbandwidth int    `json:"max_bandwidth"`
	Minbandwidth int    `json:"min_bandwidth"`
}

type ControlServer struct {
	Allow      bool   `json:"allow_control"`
	ServerAddr string `json:"server_addr"`
	AccessKey  string `json:"access_key"`
}

type UpServer struct {
	NodeName  string `json:"node_name,omitempty"`
	Address   string `json:"address"`
	Bandwidth uint64 `json:"bandwidth,omitempty"`
}

type UpLoadChannels struct {
	ChannelName     string     `json:"channel_name"`
	ChannelUrl      string     `json:"channel_url"`
	FregDuration    int        `json:"fregment_duration"`
	FregWindow      int        `json:"fregment_window"`
	FregExtraWindow int        `json:"fregment_extra_window"`
	IngestUrls      []string   `json:"ingest_urls"`
	UploadServers   []UpServer `json:"upload_servers"`
	Chaninfo        ChanInfo   `json:"channel_info,omitempty"`
}

type Profile struct {
	Bopen   bool   `json:"open"`
	BindAdr string `json:"bind_addr"`
}

type Config struct {
	NodeName        string           `json:"node_name"`
	LogLevel        int              `json:"log_level"`
	ListenAddr      string           `json:"listen_addr"`
	Serverbandwidth uint64           `json:"server_bandwidth"`
	UploadBase      string           `json:"upload_url_base"`
	Pro             Profile          `json:"profile,omitempty"`
	Control         ControlServer    `json:"source_ctrl_server,omitempty"`
	Tunnel          TunnelConfig     `json:"tunnel_config,omitempty"`
	Channels        []UpLoadChannels `json:"upload_channels,omitempty"`
}

func parseJSONConfig(config *Config, path string) error {
	file, err := os.Open(path) // For read access.
	if err != nil {
		return err
	}
	defer file.Close()

	return json.NewDecoder(file).Decode(config)
}
