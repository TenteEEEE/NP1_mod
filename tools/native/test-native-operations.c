#include "operations-frame.h"
static unsigned char data[1024];
static unsigned values[8],seen;
unsigned char*ncc_ops_buffer(void){return data;}
int ncc_ops_parse(unsigned size,unsigned pid,unsigned nonce,unsigned function,unsigned command){return ops_frame(data,size,pid,"DriveRec",nonce,function,command,values,&seen);}
unsigned ncc_ops_value(unsigned field){return field<8?values[field]:0;}
unsigned ncc_ops_seen(void){return seen;}
