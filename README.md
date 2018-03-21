# hlsuploader
Can convert any H264/HEVC video stream to HLS,and synchronizing send to mutiple servers using (http-dav) method,Such as nginx

Purpose: this project is used for live-p2p resource transmisstion.

Related projects:
https://github.com/FFmpeg/FFmpeg

API control:
传输查询接口

1.查询所有节目信息

HTTP GET  /api/query_allchannelinfo

{
    "node_name":"trans_first_1",
    channels:[
        {
            "channel_name":"test1",
            "channel_url":"/live/zhongwen/CCTV1/playlist",
            "channel_bandwidth":3513,
            "channel_info":"Video=h264 1920x1080 25r 3100k Audio=aac 44.1 240k",
            "current_ingest_url":"http://192.168.1.53:1052/test1.ts",
            "ingest_urls":[
                "http://192.168.1.53:1052/test1.ts",
                "http://192.168.1.52:1052/test1.ts",
                "http://192.168.1.54:1052/test1.ts"
            ],
            "post_servers":[
                "192.168.3.135:1354",
                "192.168.3.132:1354",
                "192.168.3.131:1354",
            ]
        },
        {
            "channel_name":"test2",
            "channel_url":"/live/zhongwen/CCTV2/playlist",
            "channel_bandwidth":3513,
            "channel_info":"Video=h264 1920x1080 25r 3100k Audio=aac 44.1 240k",
            "current_ingest_url":"http://192.168.1.53:1052/test2.ts",
            "ingest_urls":[
                "http://192.168.1.53:1052/test2.ts",
                "http://192.168.1.52:1052/test2.ts",
                "http://192.168.1.54:1052/test2.ts"
            ],
            "post_servers":[
                "192.168.3.135:1354",
                "192.168.3.132:1354",
                "192.168.3.131:1354",
            ]
        }
    ]
}

2.查询当前节目传输情况 url=(base64)

HTTP GET /api/query_channel_transinfo&url=L2xpdmUvemhvbmd3ZW4vQ0NUVjEvcGxheWxpc3Q=     (/live/zhongwen/CCTV1/playlist)

{
    "node_name":"trans_first_1",
    "channel_name":"test1",
    "channel_url":"/live/zhongwen/CCTV1/playlist",
    "fregment_duration":"10s",
    "fregment_window":3,
    "fregment_extra_window":2,

    "channel_bandwidth":3513,
    "channel_info":"Video=HEVC 1920x1080 25r 3100k Audio=aac 44.1 240k",
    "current_ingest_url":"http://192.168.1.53:1052/test1.ts",
	"stream_bandwidth":514041,
    "recv_stream_duration":51233,
    "recv_video_frames":2351231,
    "recv_audio_frames":2351231,
    "recv_frame_bytes":1234515,
    "ingest_urls":[
                "http://192.168.1.53:1052/test1.ts",
                "http://192.168.1.52:1052/test1.ts",
                "http://192.168.1.54:1052/test1.ts"
    ],
    "upload_servers":[
        {
            "server_address":"192.168.1.53:2193",
            "upload_fregment":2813,
            "upload_failed_fregment":153,
            "upload_fregment_list":2812,
            "upload_failed_fregment_list":3,
            "current_delay":15,
            "upload_file_max_time":"25083ms",
            "upload_file_min_time":"1320ms",
            "upload_file_avg_time":"2518ms"
        },
        {
            "server_address":"192.168.1.54:2193",
            "upload_fregment":2813,
            "upload_failed_fregment":153,
            "upload_fregment_list":2812,
            "upload_failed_fregment_list":3,
            "current_delay":15,
            "upload_file_max_time":"25083ms",
            "upload_file_min_time":"1320ms",
            "upload_file_avg_time":"2518ms"
        }
    ]
}


传输临时控制接口（不可保存配置）

HTTP POST /api/channel_api_control

添加节目

{
    "cmd":"add_channel",
    "channel_name":"test1",
    "channel_url":"/live/zhongwen/CCTV1/playlist",
    "fregment_duration":10000,
    "fregment_window":3,
    "fregment_extra_window":2,
    "ingest_urls":[
        "http://192.168.1.53:1052/test1.ts",
        "http://192.168.1.52:1052/test1.ts",
        "http://192.168.1.54:1052/test1.ts"
    ],
    "upload_servers":[
		{
			"node_name":"us_edge_1",
			"address":"192.168.1.23:2193/1052:1062"
		},
		{
			"node_name":"us_edge_2",
			"address":"192.168.1.24:2193/1052:1062"
		}
    ]
}

删除节目

{
    "cmd":"del_channel",
    "channel_name":"test1",
    "channel_url":"/live/zhongwen/CCTV1/playlist"
}




资源调度控制接口    unified_resource_controller

HTTP POST /api/ingest_control_cmd&key=axlkjhvalksqwoiuaddfvc

1.同步节目信息
{
    "cmd":"sync_channels",
	"upload_channels":[
        {
            "channel_name":"test1",
            "channel_url":"/live/zhongwen/cctv1/playlist",
            "fregment_duration":10000,
            "fregment_window":3,
            "fregment_extra_window":2,
            "ingest_urls":[
                "/opt/Passengers.2016.1080p.H265.mp4"
            ],
            "upload_servers":[                 
                {
					"node_name":"us_edge_1",
					"address":"192.168.10.182:8080"
				}
            ]
        }
    ]
}

