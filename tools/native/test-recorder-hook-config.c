#include "recorder-hook-config.h"
static rec_u8 buffer[NCC_RECORDER_CONFIG_SIZE];
rec_u8*ncc_recorder_buffer(void){return buffer;}
rec_u32 ncc_recorder_crc(void){return recorder_crc32(buffer,60);}
rec_u32 ncc_recorder_decode(rec_u32 field){struct ncc_recorder_config c;if(!recorder_config_decode(buffer,sizeof(buffer),&c))return 0xffffffffu;switch(field){case 1:return c.front_bitrate_bps;case 2:return c.cabin_bitrate_bps;case 3:return c.front_rate_control;case 4:return c.cabin_rate_control;case 5:return c.front_gop_ms;case 6:return c.cabin_gop_ms;case 7:return c.cabin_enabled;case 8:return c.generation;case 9:return c.segment_duration_seconds;default:return 0xffffffffu;}}
