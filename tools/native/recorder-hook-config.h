#ifndef NCC_RECORDER_HOOK_CONFIG_H
#define NCC_RECORDER_HOOK_CONFIG_H

typedef unsigned char rec_u8;
typedef unsigned int rec_u32;
typedef unsigned long rec_size;

#define NCC_RECORDER_CONFIG_SIZE 64u
#define NCC_RECORDER_CONFIG_VERSION 1u
#define NCC_RECORDER_RATE_CBR 1u
#define NCC_RECORDER_RATE_VBR 2u

struct ncc_recorder_config {
 rec_u32 front_bitrate_bps;
 rec_u32 cabin_bitrate_bps;
 rec_u32 front_rate_control;
 rec_u32 cabin_rate_control;
 rec_u32 front_gop_ms;
 rec_u32 cabin_gop_ms;
 rec_u32 cabin_enabled;
 rec_u32 generation;
};

static rec_u32 recorder_le32(const rec_u8*p){return (rec_u32)p[0]|((rec_u32)p[1]<<8)|((rec_u32)p[2]<<16)|((rec_u32)p[3]<<24);}
static rec_u32 recorder_crc32(const rec_u8*p,rec_size n){rec_u32 crc=0xffffffffu;while(n--){crc^=*p++;for(int i=0;i<8;i++)crc=(crc>>1)^(0xedb88320u&((rec_u32)0-(crc&1u)));}return ~crc;}
static int recorder_config_valid_value(const struct ncc_recorder_config*c){
 return c->front_bitrate_bps>=128000u&&c->front_bitrate_bps<=50000000u&&
  c->cabin_bitrate_bps>=128000u&&c->cabin_bitrate_bps<=50000000u&&
  (c->front_rate_control==NCC_RECORDER_RATE_CBR||c->front_rate_control==NCC_RECORDER_RATE_VBR)&&
  (c->cabin_rate_control==NCC_RECORDER_RATE_CBR||c->cabin_rate_control==NCC_RECORDER_RATE_VBR)&&
  c->front_gop_ms>=250u&&c->front_gop_ms<=10000u&&c->cabin_gop_ms>=250u&&c->cabin_gop_ms<=10000u&&
  c->cabin_enabled<=1u&&c->generation!=0u;
}
static int recorder_config_decode(const rec_u8*b,rec_size n,struct ncc_recorder_config*out){
 static const rec_u8 magic[8]={'N','C','C','R','E','C','1',0};
 if(!b||!out||n!=NCC_RECORDER_CONFIG_SIZE)return 0;
 for(int i=0;i<8;i++)if(b[i]!=magic[i])return 0;
 if(recorder_le32(b+8)!=NCC_RECORDER_CONFIG_VERSION||recorder_le32(b+12)!=NCC_RECORDER_CONFIG_SIZE||recorder_crc32(b,60)!=recorder_le32(b+60))return 0;
 out->front_bitrate_bps=recorder_le32(b+16);out->cabin_bitrate_bps=recorder_le32(b+20);
 out->front_rate_control=recorder_le32(b+24);out->cabin_rate_control=recorder_le32(b+28);
 out->front_gop_ms=recorder_le32(b+32);out->cabin_gop_ms=recorder_le32(b+36);
 out->cabin_enabled=recorder_le32(b+40);out->generation=recorder_le32(b+56);
 return !recorder_le32(b+44)&&!recorder_le32(b+48)&&!recorder_le32(b+52)&&recorder_config_valid_value(out);
}
#endif
