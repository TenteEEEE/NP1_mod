/* DriveRec startup hook for the single pinned NP1 firmware.
 * The stock executable and its files remain untouched. Unknown firmware,
 * instructions or configuration always retain stock behavior. */
#include "native-core.h"
#include "recorder-hook-config.h"

typedef long ssize_t;
extern int open(const char*,int,...);
extern int close(int);
extern ssize_t read(int,void*,size_t);
extern ssize_t write(int,const void*,size_t);
extern int mprotect(void*,size_t,int);
extern void*mmap(void*,size_t,int,int,int,long);
extern unsigned long getauxval(unsigned long);
extern void*dlsym(void*,const char*);
extern int socket(int,int,int);
extern int connect(int,const void*,unsigned);
extern ssize_t sendmsg(int,const void*,int);
extern void _exit(int);

#define O_RDONLY 0
#define O_CLOEXEC 02000000
#define PROT_READ 1
#define PROT_WRITE 2
#define PROT_EXEC 4
#define MAP_PRIVATE 2
#define MAP_ANONYMOUS 0x20
#define MAP_FAILED ((void*)-1)
#define RTLD_NEXT ((void*)-1L)
#define AT_PHDR 3
#define AF_UNIX 1
#define SOCK_DGRAM 2
#define SOCK_NONBLOCK 04000
#define SOCK_CLOEXEC 02000000
#define MSG_DONTWAIT 0x40
#define MSG_NOSIGNAL 0x4000
#define LIVE_HEADER_SIZE 56
#define LIVE_CHUNK_SIZE 60000
#define LIVE_MAX_FRAME (4u*1024u*1024u)
#define DRIVE_REC_SHA "000871dc36581f27ae831d9db3772b553a17cce0d814c52867276ac68ea0cde0"
#define CONFIG_PATH "/overlay/owner-apps/np1-control/recorder-hook/config"

static int firmware_ok;
static void*start_one_trampoline;
static void*start_two_trampoline;
static void*stop_trampoline;
static void(*stream_write_original)(void*,void*);

struct ncc_iovec {void*base;size_t length;};
struct ncc_msghdr {void*name;unsigned name_length;struct ncc_iovec*iov;size_t iov_length;void*control;size_t control_length;int flags;};
struct ncc_sockaddr_un {unsigned short family;char path[108];};
struct live_source {
 void*objects[3];
 u32 object_count;
 void*active_object;
 int fd;
 u32 source;
 u32 frame_sequence;
 u32 width;
 u32 height;
 u32 fps_milli;
 u8 sps[1024];
 u8 pps[1024];
 u32 sps_size;
 u32 pps_size;
 u8 config[2056];
 u32 config_size;
 int config_sent;
 int observed;
};
static struct live_source live_front={{0},0,0,-1,1,0,0,0,0,{0},{0},0,0,{0},0,0,0};
static struct live_source live_cabin={{0},0,0,-1,2,0,0,0,0,{0},{0},0,0,{0},0,0,0};

static void bytes_copy(void*destination,const void*source,size_t n){u8*d=destination;const u8*s=source;while(n--)*d++=*s++;}
static int bytes_equal(const void*a,const void*b,size_t n){const u8*x=a,*y=b;while(n--)if(*x++!=*y++)return 0;return 1;}
static void log_line(const char*message){size_t n=0;while(message[n])n++;(void)write(2,message,n);}
static void log_u32(u32 value){char text[16];size_t n=0;if(!value)text[n++]='0';else{char reverse[10];size_t used=0;while(value){reverse[used++]=(char)('0'+value%10);value/=10;}while(used)text[n++]=reverse[--used];}text[n++]='\n';(void)write(2,text,n);}
static int digest_file(const char*path,const char*expected){
 int fd=open(path,O_RDONLY|O_CLOEXEC);if(fd<0)return 0;struct sha_state state;sha_init(&state);u8 buffer[4096],digest[32];unsigned long total=0;
 for(;;){ssize_t n=read(fd,buffer,sizeof(buffer));if(n<0||total>64UL*1024*1024){close(fd);return 0;}if(!n)break;total+=(unsigned long)n;sha_update(&state,buffer,(size_t)n);}close(fd);sha_final(&state,digest);
 const char*hex="0123456789abcdef";for(int i=0;i<32;i++)if(expected[2*i]!=hex[digest[i]>>4]||expected[2*i+1]!=hex[digest[i]&15])return 0;return 1;
}
static int load_config(struct ncc_recorder_config*out){u8 data[NCC_RECORDER_CONFIG_SIZE];int fd=open(CONFIG_PATH,O_RDONLY|O_CLOEXEC);if(fd<0)return 0;size_t used=0;while(used<sizeof(data)){ssize_t n=read(fd,data+used,sizeof(data)-used);if(n<=0){close(fd);return 0;}used+=(size_t)n;}u8 extra;if(read(fd,&extra,1)!=0){close(fd);return 0;}close(fd);return recorder_config_decode(data,sizeof(data),out);}

static void cache_sync(void*begin,void*end){
#if defined(__aarch64__)
 unsigned long ctr;__asm__ volatile("mrs %0, ctr_el0":"=r"(ctr));unsigned long dline=4UL<<((ctr>>16)&15),iline=4UL<<(ctr&15);
 unsigned long start=(unsigned long)begin,finish=(unsigned long)end;for(unsigned long p=start&~(dline-1);p<finish;p+=dline)__asm__ volatile("dc cvau, %0"::"r"(p):"memory");__asm__ volatile("dsb ish":::"memory");for(unsigned long p=start&~(iline-1);p<finish;p+=iline)__asm__ volatile("ic ivau, %0"::"r"(p):"memory");__asm__ volatile("dsb ish\nisb":::"memory");
#else
 (void)begin;(void)end;
#endif
}
static void emit_u32(u8*p,u32 value){p[0]=(u8)value;p[1]=(u8)(value>>8);p[2]=(u8)(value>>16);p[3]=(u8)(value>>24);}
static void emit_u64(u8*p,unsigned long value){emit_u32(p,(u32)value);emit_u32(p+4,(u32)(value>>32));}
static int annex_start(const u8*data,u32 size,u32 from,u32*prefix){
 for(u32 i=from;i+3<=size;i++)if(data[i]==0&&data[i+1]==0){if(data[i+2]==1){*prefix=3;return (int)i;}if(i+4<=size&&data[i+2]==0&&data[i+3]==1){*prefix=4;return (int)i;}}
 return -1;
}
static void update_parameter_cache(struct live_source*source,const u8*data,u32 size){
 u32 prefix=0;int start=annex_start(data,size,0,&prefix);
 while(start>=0){u32 payload=(u32)start+prefix,next_prefix=0;int next=annex_start(data,size,payload,&next_prefix);u32 end=next<0?size:(u32)next;while(end>payload&&data[end-1]==0)end--;if(end>payload){u32 n=end-payload,type=data[payload]&31;if(type==7&&n<=sizeof(source->sps)){bytes_copy(source->sps,data+payload,n);source->sps_size=n;}else if(type==8&&n<=sizeof(source->pps)){bytes_copy(source->pps,data+payload,n);source->pps_size=n;}}start=next;prefix=next_prefix;
 }
 if(source->sps_size&&source->pps_size){u32 at=0;source->config[at++]=0;source->config[at++]=0;source->config[at++]=0;source->config[at++]=1;bytes_copy(source->config+at,source->sps,source->sps_size);at+=source->sps_size;source->config[at++]=0;source->config[at++]=0;source->config[at++]=0;source->config[at++]=1;bytes_copy(source->config+at,source->pps,source->pps_size);at+=source->pps_size;source->config_size=at;}
}
static int live_connect(struct live_source*source){
 if(source->fd>=0)return 1;int fd=socket(AF_UNIX,SOCK_DGRAM|SOCK_NONBLOCK|SOCK_CLOEXEC,0);if(fd<0)return 0;
 struct ncc_sockaddr_un address;address.family=AF_UNIX;for(size_t i=0;i<sizeof(address.path);i++)address.path[i]=0;const char*path=source->source==1?"/run/np1-control/camera-encoder-front.sock":"/run/np1-control/camera-encoder-in.sock";size_t n=0;while(path[n]&&n+1<sizeof(address.path)){address.path[n]=path[n];n++;}if(path[n]||connect(fd,&address,(unsigned)sizeof(address))){close(fd);return 0;}source->fd=fd;source->config_sent=0;return 1;
}
static int live_send(struct live_source*source,const u8*data,u32 size,u32 sequence,unsigned long pts,u32 nal_type){
 if(!size||size>LIVE_MAX_FRAME||!live_connect(source))return 0;
 for(u32 offset=0;offset<size;){u32 chunk=size-offset;if(chunk>LIVE_CHUNK_SIZE)chunk=LIVE_CHUNK_SIZE;u8 header[LIVE_HEADER_SIZE];for(u32 i=0;i<LIVE_HEADER_SIZE;i++)header[i]=0;header[0]='N';header[1]='C';header[2]='C';header[3]='A';header[4]='U';header[5]='0';header[6]='0';header[7]='1';emit_u32(header+8,source->source);emit_u32(header+12,LIVE_HEADER_SIZE);emit_u32(header+16,sequence);emit_u32(header+20,size);emit_u32(header+24,offset);emit_u32(header+28,chunk);emit_u32(header+32,source->width);emit_u32(header+36,source->height);emit_u32(header+40,source->fps_milli);emit_u32(header+44,nal_type);emit_u64(header+48,pts);struct ncc_iovec vectors[2]={{header,sizeof(header)},{(void*)(data+offset),chunk}};struct ncc_msghdr message={0,0,vectors,2,0,0,0};ssize_t written=sendmsg(source->fd,&message,MSG_DONTWAIT|MSG_NOSIGNAL);if(written!=(ssize_t)(sizeof(header)+chunk)){close(source->fd);source->fd=-1;source->config_sent=0;source->active_object=0;return 0;}offset+=chunk;}
 return 1;
}
static void hook_stream_write(void*self,void*packet){
 struct live_source*source=0;for(u32 i=0;i<live_front.object_count;i++)if(self==live_front.objects[i])source=&live_front;for(u32 i=0;i<live_cabin.object_count;i++)if(self==live_cabin.objects[i])source=&live_cabin;
 if(source&&packet){const u8*data=*(const u8**)packet;unsigned long length=*(unsigned long*)((u8*)packet+8);if(data&&length>0&&length<=LIVE_MAX_FRAME){if(!source->active_object){update_parameter_cache(source,data,(u32)length);if(live_connect(source)){source->active_object=self;if(!source->observed){source->observed=1;log_line(source->source==1?"ncc-recorder-hook: front encoder output observed\n":"ncc-recorder-hook: cabin encoder output observed\n");}}}if(source->active_object==self){update_parameter_cache(source,data,(u32)length);if(!source->config_sent&&source->config_size&&live_send(source,source->config,source->config_size,0,0,0))source->config_sent=1;source->frame_sequence++;if(!source->frame_sequence)source->frame_sequence=1;(void)live_send(source,data,(u32)length,source->frame_sequence,*(unsigned long*)((u8*)packet+24),(u32)*(unsigned long*)((u8*)packet+32));}}}
 stream_write_original(self,packet);
}
static void install_stream_write_hook(void){
 void*vtable=dlsym(RTLD_NEXT,"_ZTV16MPS_StreamBuffer");void*original=dlsym(RTLD_NEXT,"_ZN16MPS_StreamBuffer5writeEO16MPS_StreamPacket");if(!vtable||!original){log_line("ncc-recorder-hook: stream buffer symbols unavailable; live tap disabled\n");return;}void**slot=(void**)((u8*)vtable+40);if(*slot!=original){log_line("ncc-recorder-hook: stream buffer vtable mismatch; live tap disabled\n");return;}void*page=(void*)((unsigned long)slot&~4095UL);if(mprotect(page,4096,PROT_READ|PROT_WRITE)){log_line("ncc-recorder-hook: stream buffer vtable RW protection failed; live tap disabled\n");return;}stream_write_original=(void(*)(void*,void*))original;*slot=(void*)hook_stream_write;if(mprotect(page,4096,PROT_READ)){*slot=original;if(mprotect(page,4096,PROT_READ))_exit(78);stream_write_original=0;log_line("ncc-recorder-hook: stream buffer vtable protection restore failed; live tap disabled\n");return;}log_line("ncc-recorder-hook: encoder output live tap installed\n");
}
static void absolute_jump(u8*p,void*target){emit_u32(p,0x58000050u);emit_u32(p+4,0xd61f0200u);emit_u64(p+8,(unsigned long)target);}
static void*simple_trampoline(u8*space,const u8*original,void*resume){bytes_copy(space,original,16);absolute_jump(space+16,resume);return space;}
static void*stop_function_trampoline(u8*space,const u8*original,void*resume,void*rodata_page){
 bytes_copy(space,original,12);emit_u32(space+12,0x580000a4u);emit_u32(space+16,0x580000d0u);emit_u32(space+20,0xd61f0200u);emit_u32(space+24,0xd503201fu);emit_u32(space+28,0xd503201fu);emit_u64(space+32,(unsigned long)rodata_page);emit_u64(space+40,(unsigned long)resume);return space;
}
static void install_memory_hooks(void);

static int cabin_disabled(void){struct ncc_recorder_config config;return load_config(&config)&&!config.cabin_enabled;}
static int hook_start_one(void*self,unsigned camera){if(camera==1&&cabin_disabled()){log_line("ncc-recorder-hook: cabin ordinary start suppressed\n");return 1;}return ((int(*)(void*,unsigned))start_one_trampoline)(self,camera);}
static int hook_start_two(void*self,unsigned camera,unsigned option){if(camera==1&&cabin_disabled()){log_line("ncc-recorder-hook: cabin ordinary start suppressed\n");return 1;}return ((int(*)(void*,unsigned,unsigned))start_two_trampoline)(self,camera,option);}
static int hook_stop(void*self,unsigned camera){if(camera==1&&cabin_disabled()){log_line("ncc-recorder-hook: cabin ordinary stop suppressed\n");return 1;}return ((int(*)(void*,unsigned))stop_trampoline)(self,camera);}

static void patch_jump(void*address,void*target){absolute_jump((u8*)address,target);}
static u32 mov_w6_immediate(u32 value){return 0x52800006u|(value<<5);}
static void install_memory_hooks(void){
 unsigned long phdr=getauxval(AT_PHDR);if(phdr<64){log_line("ncc-recorder-hook: main program header unavailable\n");return;}u8*base=(u8*)(phdr-64);
 static const u8 elf_prefix[8]={0x7f,'E','L','F',2,1,1,3};if(!bytes_equal(base,elf_prefix,8)){log_line("ncc-recorder-hook: main image base mismatch\n");return;}
 u8*one=base+0x138378,*two=base+0x1384d8,*stop=base+0x138658,*duration=base+0x14c774;
 static const u8 one_expected[16]={0xe9,0x13,0x40,0xd1,0x3f,0xd9,0x07,0xf9,0xfd,0x7b,0xbb,0xa9,0xfd,0x03,0x00,0x91};
 static const u8 two_expected[16]={0xe9,0x13,0x40,0xd1,0x3f,0xd1,0x07,0xf9,0xfd,0x7b,0xba,0xa9,0xe4,0x03,0x01,0x2a};
 static const u8 stop_expected[16]={0xe9,0x13,0x40,0xd1,0x3f,0xd9,0x07,0xf9,0xfd,0x7b,0xbb,0xa9,0x64,0x03,0x00,0xd0};
 static const u8 duration_expected[4]={0x86,0x16,0x80,0x52};
 if(!bytes_equal(one,one_expected,16)||!bytes_equal(two,two_expected,16)||!bytes_equal(stop,stop_expected,16)||!bytes_equal(duration,duration_expected,4)){log_line("ncc-recorder-hook: instruction fingerprint mismatch; stock behavior retained\n");return;}
 log_line("ncc-recorder-hook: firmware and instruction fingerprints verified\n");
 u8*tramp=mmap(0,4096,PROT_READ|PROT_WRITE,MAP_PRIVATE|MAP_ANONYMOUS,-1,0);if(tramp==MAP_FAILED){log_line("ncc-recorder-hook: trampoline allocation failed\n");return;}
 start_one_trampoline=simple_trampoline(tramp,one,one+16);start_two_trampoline=simple_trampoline(tramp+64,two,two+16);stop_trampoline=stop_function_trampoline(tramp+128,stop,stop+16,base+0x1a6000);
 cache_sync(tramp,tramp+192);if(mprotect(tramp,4096,PROT_READ|PROT_EXEC)){log_line("ncc-recorder-hook: trampoline RX protection failed\n");return;}
 /* This runs in the loader before DriveRec main and before worker threads.
  * Never request simultaneous W+X: make the target page non-executable while
  * updating it, then restore RX before returning from the constructor. */
 void*page=(void*)((unsigned long)one&~4095UL);if(mprotect(page,4096,PROT_READ|PROT_WRITE)){log_line("ncc-recorder-hook: text RW protection failed\n");return;}
 patch_jump(one,(void*)hook_start_one);patch_jump(two,(void*)hook_start_two);patch_jump(stop,(void*)hook_stop);cache_sync(one,stop+16);
 if(mprotect(page,4096,PROT_READ|PROT_EXEC)){
  bytes_copy(one,one_expected,16);bytes_copy(two,two_expected,16);bytes_copy(stop,stop_expected,16);cache_sync(one,stop+16);
  if(mprotect(page,4096,PROT_READ|PROT_EXEC))_exit(78);
  log_line("ncc-recorder-hook: text protection restore failed; original instructions restored\n");
 }
 log_line("ncc-recorder-hook: ordinary recording memory hooks installed\n");
 struct ncc_recorder_config config;u32 segment_duration=180u;
 if(load_config(&config))segment_duration=config.segment_duration_seconds;
 void*duration_page=(void*)((unsigned long)duration&~4095UL);if(mprotect(duration_page,4096,PROT_READ|PROT_WRITE)){log_line("ncc-recorder-hook: duration text RW protection failed; stock duration retained\n");return;}
 emit_u32(duration,mov_w6_immediate(segment_duration));cache_sync(duration,duration+4);
 if(mprotect(duration_page,4096,PROT_READ|PROT_EXEC)){
  bytes_copy(duration,duration_expected,4);cache_sync(duration,duration+4);
  if(mprotect(duration_page,4096,PROT_READ|PROT_EXEC))_exit(78);
  log_line("ncc-recorder-hook: duration text protection restore failed; stock duration restored\n");return;
 }
 log_line("ncc-recorder-hook: ordinary segment duration applied seconds=");log_u32(segment_duration);
}

typedef void(*create_streamer_fn)(void*,void*,void*,void*,void*,void*,void*);
/* Pinned NPMultimediaPlayerVideoEncodeProperty ABI. DriveRec copies this
 * 40-byte value to the x3 indirect argument immediately before the call at
 * 0x165fa8. The final u32 is ABI padding and must never be modified. */
struct video_encode_property {
 double frame_rate;
 u32 width;
 u32 height;
 u32 codec_type;
 u32 abi_padding;
 u32 bitrate_type;
 u32 bitrate_bps;
 u32 keyframe_interval_ms;
 u32 tail_padding;
};
_Static_assert(sizeof(struct video_encode_property)==40,"unexpected video encode property ABI");
static const char create_streamer_symbol[]="_Z31NPMPS_CreateVideoEncodeStreamerRSt10unique_ptrI31NPMultimediaPlayerStackStreamerSt14default_deleteIS0_EE37NPMultimediaPlayerVideoStreamPropertySt10shared_ptrI12MPS_IOStreamE37NPMultimediaPlayerVideoEncodePropertyS8_St6vectorI25NPMultimediaPlayerMixDataSaISB_EENSt7__cxx1112basic_stringIcSt11char_traitsIcESaIcEEE";
void ncc_create_streamer(void*a,void*b,void*c,void*encode,void*e,void*f,void*g) __asm__("_Z31NPMPS_CreateVideoEncodeStreamerRSt10unique_ptrI31NPMultimediaPlayerStackStreamerSt14default_deleteIS0_EE37NPMultimediaPlayerVideoStreamPropertySt10shared_ptrI12MPS_IOStreamE37NPMultimediaPlayerVideoEncodePropertyS8_St6vectorI25NPMultimediaPlayerMixDataSaISB_EENSt7__cxx1112basic_stringIcSt11char_traitsIcESaIcEEE");
void ncc_create_streamer(void*a,void*b,void*c,void*encode,void*e,void*f,void*g){
 create_streamer_fn real=(create_streamer_fn)dlsym(RTLD_NEXT,create_streamer_symbol);if(!real){log_line("ncc-recorder-hook: original streamer symbol unavailable\n");_exit(79);}
 if(firmware_ok&&encode){
  struct video_encode_property*property=(struct video_encode_property*)encode;
  u32 stock_bitrate=property->bitrate_bps;
  log_line("ncc-recorder-hook: video streamer intercepted, stock bitrate=");log_u32(property->bitrate_bps);
  struct ncc_recorder_config config;
  int config_ok=load_config(&config);
  /* The native quality presets create front 1080p/10 Mbps streams and cabin
   * 1080p/4 Mbps or 720p/2 Mbps streams. Width, height, codec and stock rate
   * are all required so an unrelated encoder retains stock behavior. */
  int front=property->width==1920u&&property->height==1080u&&stock_bitrate==10000000u;
  int cabin=(property->width==1920u&&property->height==1080u&&stock_bitrate==4000000u)||(property->width==1280u&&property->height==720u&&stock_bitrate==2000000u);
  if(property->codec_type==9u&&(front||cabin)&&config_ok){
   struct live_source*source=front?&live_front:&live_cabin;
   if(front){property->bitrate_type=config.front_rate_control;property->bitrate_bps=config.front_bitrate_bps;property->keyframe_interval_ms=config.front_gop_ms;log_line("ncc-recorder-hook: front encoder bitrate applied=");log_u32(property->bitrate_bps);log_line("ncc-recorder-hook: front encoder rate control applied=");log_u32(property->bitrate_type);log_line("ncc-recorder-hook: front encoder gop applied=");log_u32(property->keyframe_interval_ms);}
   else{property->bitrate_type=config.cabin_rate_control;property->bitrate_bps=config.cabin_bitrate_bps;property->keyframe_interval_ms=config.cabin_gop_ms;log_line("ncc-recorder-hook: cabin encoder bitrate applied=");log_u32(property->bitrate_bps);log_line("ncc-recorder-hook: cabin encoder rate control applied=");log_u32(property->bitrate_type);log_line("ncc-recorder-hook: cabin encoder gop applied=");log_u32(property->keyframe_interval_ms);}
   if(e&&source->object_count<3){void*object=*(void**)e;if(object){source->objects[source->object_count++]=object;source->width=property->width;source->height=property->height;source->fps_milli=(u32)(property->frame_rate*1000.0+0.5);void*vtable=dlsym(RTLD_NEXT,"_ZTV16MPS_StreamBuffer");log_line(vtable&&*(void**)object==(u8*)vtable+16?"ncc-recorder-hook: output object is stream buffer\n":"ncc-recorder-hook: output object vtable mismatch\n");log_line(source->source==1?"ncc-recorder-hook: front encoder output registered\n":"ncc-recorder-hook: cabin encoder output registered\n");}}
  }else log_line("ncc-recorder-hook: encoder ABI guard mismatch; stock settings retained\n");
 }
 real(a,b,c,encode,e,f,g);
}

__attribute__((constructor)) static void recorder_hook_init(void){firmware_ok=digest_file("/proc/self/exe",DRIVE_REC_SHA);if(!firmware_ok){log_line("ncc-recorder-hook: firmware mismatch; stock behavior retained\n");return;}install_memory_hooks();install_stream_write_hook();}
