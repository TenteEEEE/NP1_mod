#ifndef NCC_OPERATIONS_FRAME_H
#define NCC_OPERATIONS_FRAME_H
/* Bounded CommonFreeStyle correlation. The response must come from the pinned
 * owner's PID and name, and match this request's nonce, function and command. */
static unsigned ops16(const unsigned char*p){return p[0]|((unsigned)p[1]<<8);}
static unsigned ops32(const unsigned char*p){return ops16(p)|((unsigned)ops16(p+2)<<16);}
static unsigned long long ops64(const unsigned char*p){return ops32(p)|((unsigned long long)ops32(p+4)<<32);}
static int ops_varint(const unsigned char*b,unsigned long end,unsigned long*p,unsigned*v){unsigned long long value=0;for(unsigned i=0;i<10;i++){if(*p>=end)return 0;unsigned c=b[(*p)++];if(i==9&&c>1)return 0;value|=(unsigned long long)(c&127)<<(i*7);if(!(c&128)){if(i&&!(c&127))return 0;if(value>0xffffffffULL&&value<0xffffffff80000000ULL)return 0;*v=(unsigned)value;return 1;}}return 0;}
static inline int ops_payload(const unsigned char*b,unsigned long size,unsigned pid,const char*owner,unsigned nonce,unsigned function,unsigned command,unsigned long*start){
 if(!b||!owner||!start||size<40||size>8192||ops64(b)!=0x80001f02ULL||ops32(b+8)!=pid)return 0;
 unsigned chars=ops32(b+14),count=0;while(owner[count])count++;if(chars!=count+1||chars>32)return 0;
 unsigned long p=18+chars*2;if(p+20>size)return 0;for(unsigned i=0;i<chars;i++)if(ops16(b+18+2*i)!=(unsigned char)owner[i])return 0;
 if(ops32(b+p)!=0x4e43434f||ops32(b+p+4)!=nonce||ops16(b+p+8)!=function||ops16(b+p+10)!=command||ops64(b+p+12)!=size-p-20)return 0;
 *start=p+20;return 1;
}
int ops_frame(const unsigned char*b,unsigned long size,unsigned pid,const char*owner,unsigned nonce,unsigned function,unsigned command,unsigned*values,unsigned*seen){
 if(!b||!owner||!values||!seen||size<40||size>512||ops64(b)!=0x80001f02ULL||ops32(b+8)!=pid)return 0;
 unsigned chars=ops32(b+14),count=0;while(owner[count])count++;if(chars!=count+1||chars>32)return 0;
 unsigned long p=18+chars*2;if(p+20>size)return 0;for(unsigned i=0;i<chars;i++)if(ops16(b+18+2*i)!=(unsigned char)owner[i])return 0;
 if(ops32(b+p)!=0x4e43434f||ops32(b+p+4)!=nonce||ops16(b+p+8)!=function||ops16(b+p+10)!=command||ops64(b+p+12)!=size-p-20)return 0;
 p+=20;unsigned mask=0;for(unsigned i=0;i<8;i++)values[i]=0;
 while(p<size){unsigned key,value;if(!ops_varint(b,size,&p,&key)||(key&7)||key<8||key>56||!ops_varint(b,size,&p,&value))return 0;unsigned field=key>>3;if(mask&(1u<<field))return 0;mask|=1u<<field;values[field]=value;}
 *seen=mask;return 1;
}
#endif
