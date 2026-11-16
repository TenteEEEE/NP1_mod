#include "operations-frame.h"
static int same(const char*a,const char*b){while(*a&&*a==*b){a++;b++;}return *a==*b;}
#include "operations-actions.h"
static unsigned char data[8192];
static unsigned values[8],seen;
unsigned char*ncc_ops_buffer(void){return data;}
int ncc_ops_parse(unsigned size,unsigned pid,unsigned nonce,unsigned function,unsigned command){return ops_frame(data,size,pid,"DriveRec",nonce,function,command,values,&seen);}
unsigned ncc_ops_value(unsigned field){return field<8?values[field]:0;}
unsigned ncc_ops_seen(void){return seen;}
int ncc_ops_raw(unsigned size){unsigned long start;return ops_payload(data,size,321,"DriveRec",42,0x601,0x8002,&start);}
unsigned ncc_ops_action_count(void){return sizeof(actions)/sizeof(actions[0]);}
int ncc_ops_action(unsigned index,unsigned size){if(index>=ncc_ops_action_count()||size>sizeof(data))return 0;const struct action_spec*a=find_action(actions[index].id);return a&&action_input(a,data,size);}
