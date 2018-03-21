#ifndef __TR_MPEGEXPORT_H__
#define __TR_MPEGEXPORT_H__
#include <stdint.h>
#ifdef  __cplusplus
#define CPULS_DECLARE_BEGIN  extern "C" {
#define CPULS_DECLARE_END    }
#else
#define CPULS_DECLARE_BEGIN
#define CPULS_DECLARE_END
#endif

CPULS_DECLARE_BEGIN

struct SegMentData{
    char *pcurrenturl;
    char *pvideoinfo;
    uint8_t *pbuf;
    int nbuflen;
    int duration;
    int64_t startdts;
    int64_t enddts;
};

/*
{ 
    "channel_url":"/upload/zhongwen/CCTV2/playlist",
    "seg_duration":10000,
    "ingest_urls":[
                "http://192.168.1.53:1052/test2.ts",
                "http://192.168.1.52:1052/test2.ts",
                "http://192.168.1.54:1052/test2.ts"
    ]
} 
*/
void *Segmenter_Alloc(const char *pcmdjson);

int Segmenter_GetSegment(void *ptr,void *pseg);

int Segmenter_FreeSegment(void *ptr,void *pseg);

/*
{ 
    "ingest_urls":[
                "http://192.168.1.53:1052/test2.ts",
                "http://192.168.1.52:1052/test2.ts",
                "http://192.168.1.54:1052/test2.ts"
    ] 
} 
*/
int Segmenter_UpdateIngestUrls(void *ptr,const char *pcmdjson);

int Segmenter_UpdateDuration(void *ptr,int duration);

void Segmenter_Interrupt(void *ptr);

int Segmenter_Stoped(void *ptr);

void Segmenter_Free(void *ptr);

CPULS_DECLARE_END
#endif
