/* Fixed native app operations through existing Setting/DriveRec owners.
 * Never write vendor storage or invoke mkfs. SD format requires --format ERASE_SD. */
#include "native-alexa-runtime.h"
#include "operations-frame.h"
#include "operations-actions.h"
static char private_path[160];
static unsigned short private_map_name[96];
static int private_enabled,private_created,private_fd=-1;
static unsigned long private_device,private_inode;
static int pinned_caller(void*caller,unsigned long offset){struct ncc_dl_info i;return dladdr(caller,&i)&&same(i.file,"/usr/local/lib/libNStandardLib.so")&&(unsigned long)caller-(unsigned long)i.base==offset;}
static int private_identity(int fd){unsigned long st[16]={0};return private_created&&syscall(80,fd,st)==0&&st[0]==private_device&&st[1]==private_inode&&(le32((u8*)st+16)&0xf000)==0x8000;}
static int private_path_identity(void){int fd=(int)syscall(56,-100,private_path,0xa0000,0);if(fd<0)return 0;int same_file=private_identity(fd);syscall(57,fd);return same_file;}
static void prepare_private_name(void){
 const char*prefix="NMSG_MEM_AIPI_server_";char name[96];size_t n=0;while(prefix[n]){name[n]=prefix[n];n++;}unsigned value=(unsigned)getpid();char digits[16];size_t k=0;do{digits[k++]="0123456789ABCDEF"[value&15];value>>=4;}while(value);while(k)name[n++]=digits[--k];name[n]=0;
 for(size_t i=0;i<=n;i++)private_map_name[i]=(unsigned short)name[i];const char*root="/tmp/victoria/victoria_";size_t p=0;while(root[p]){private_path[p]=root[p];p++;}for(size_t i=0;i<n;i++)private_path[p++]=name[i];const char*end=".map";for(int i=0;i<5;i++)private_path[p++]=end[i];
 int fd=(int)syscall(56,-100,private_path,0xa0000,0);if(fd>=0){syscall(57,fd);fail("own PID receiver path already exists; no overwrite",78);}if(*__errno_location()!=2)fail("own PID receiver path cannot be checked",78);
}
static int private_name(const unsigned short*name){if(!name||!private_map_name[0])return 0;for(size_t i=0;i<96;i++){if(name[i]!=private_map_name[i])return 0;if(!name[i])return 1;}return 0;}
static int private_create(const char*path,int flags,void*caller){
 if(!private_enabled||private_created||flags!=0xc2||!same(path,private_path)||!pinned_caller(caller,0x3247c))return -2;
 int fd=(int)syscall(56,-100,path,0xa00c2,0660);if(fd<0)fail("own receiver exclusive creation failed",78);unsigned long st[16]={0};if(syscall(80,fd,st)||(le32((u8*)st+16)&0xf000)!=0x8000){syscall(57,fd);fail("own receiver file identity unavailable",78);}private_fd=fd;private_device=st[0];private_inode=st[1];private_created=1;return fd;
}
static int private_owner(int fd,unsigned uid,unsigned gid,void*caller){if(fd==private_fd&&private_identity(fd)&&uid==0xffffffffu&&pinned_caller(caller,0x32588))return (int)syscall(55,fd,uid,gid);fail("vendor ownership change outside own receiver blocked",78);return -1;}
int fchmod(int fd,unsigned mode){if(fd==private_fd&&private_identity(fd)&&mode==0660&&pinned_caller(__builtin_return_address(0),0x32494))return (int)syscall(52,fd,mode);fail("vendor chmod outside own receiver blocked",78);return -1;}
int ftruncate(int fd,long size){if(fd==private_fd&&private_identity(fd)&&size==8352&&pinned_caller(__builtin_return_address(0),0x325a0))return (int)syscall(46,fd,size);fail("vendor truncate outside own receiver blocked",78);return -1;}
static int private_unlink(const char*path,void*caller){if(private_created&&same(path,private_path)&&pinned_caller(caller,0x31f44)&&private_path_identity()){int rc=(int)syscall(35,-100,path,0);if(!rc)private_created=0;return rc;}fail("vendor unlink outside own receiver blocked",78);return -1;}
static void private_cleanup(void){if(private_created&&private_path_identity()){if(syscall(35,-100,private_path,0)==0)private_created=0;} }
static void sole_receiver_reference(void){for(unsigned i=0;i<100;i++){u8 header[128];if(read_file(private_path,header,sizeof(header))==128&&ops64(header+112)==1)return;usleep(10000);}fail("receiver peer reference did not return; cleanup requires inspection",78);}
void *tracked_memory_alloc(void*self,const unsigned short*name,long size) __asm__("_ZN17NProcSharedMemory5AllocEPKDsl");
void *tracked_memory_alloc(void*self,const unsigned short*name,long size){if(!real_memory_alloc)fail("Alloc guard bootstrap unavailable",78);if(private_name(name)){if(!private_enabled||private_created||size!=8224)fail("unexpected private receiver allocation",78);}else memory_ref_before(name);void*data=real_memory_alloc(self,name,size);if(data&&!ncc_memory_acquire((unsigned long)self)){real_memory_free(self);fail("mapping tracker capacity exceeded",78);}return data;}


struct small_string{char*pointer;unsigned long size;char local[16];};
typedef void(*common_send_fn)(void*,unsigned,unsigned,unsigned short,unsigned short,const void*);
typedef int(*queue_check_fn)(void*);
typedef int(*queue_receive_fn)(void*,u8*,unsigned,unsigned*,unsigned);
static u8 response[8192];
static unsigned request_nonce,owner_pid;
static const char*current_owner;
static const char*current_path;
static unsigned long long owner_starttime;
static unsigned parse_pid(const char*s){unsigned v=0;if(!s||!s[0])return 0;for(size_t i=0;s[i];i++){if(i>=10||s[i]<'0'||s[i]>'9'||v>214748364u)return 0;v=v*10+(unsigned)(s[i]-'0');if(v>2147483647u)return 0;}return v;}
int ops_starttime(const unsigned char*b,unsigned long size,unsigned long long*out){if(!b||!out||size<4||size>1023)return 0;unsigned long position=0;int closed=0;for(unsigned long i=0;i<size;i++)if(b[i]==')'){position=i+1;closed=1;}if(!closed)return 0;for(unsigned field=3;field<=22;field++){while(position<size&&b[position]==' ')position++;unsigned long start=position;while(position<size&&b[position]!=' '&&b[position]!='\n')position++;if(start==position)return 0;if(field==22){unsigned long long value=0;if(position-start>20)return 0;for(unsigned long i=start;i<position;i++){unsigned digit=b[i]-'0';if(digit>9||value>(0xffffffffffffffffULL-digit)/10)return 0;value=value*10+digit;}if(!value)return 0;*out=value;return 1;}}return 0;}

static void owner_live(void){char path[64],digits[16],executable[128];const char*prefix="/proc/";size_t n=0;while(prefix[n]){path[n]=prefix[n];n++;}unsigned pid=owner_pid;size_t k=0;do{digits[k++]=(char)('0'+pid%10);pid/=10;}while(pid);while(k)path[n++]=digits[--k];size_t suffix=n;const char*exe="/exe";for(size_t i=0;i<5;i++)path[n++]=exe[i];long count=syscall(78,-100,path,executable,sizeof(executable)-1);if(count<=0||count>=(long)sizeof(executable)-1)fail("Setting PID is not live",75);executable[count]=0;const char*expected_path=current_path;if(!same(executable,expected_path))fail("Setting PID executable mismatch",75);const char*stat="/stat";for(size_t i=0;i<6;i++)path[suffix+i]=stat[i];u8 data[1024];int size=read_file(path,data,sizeof(data)-1);unsigned long long start=0;if(size<=0||!ops_starttime(data,(size_t)size,&start))fail("Setting process lifetime unavailable",75);if(owner_starttime&&owner_starttime!=start)fail("Setting process restarted; no blind retry",75);owner_starttime=start;}

struct operation_spec{const char*id;const char*owner;unsigned function,get,set,field,min,max;};
static const struct operation_spec specs[]={
 {"recorder","Setting",0x600,1,0,0,0,0},{"parking","Setting",0x600,6,0,0,0,0},
 {"installation","Setting",0x700,1,0,0,0,0},{"vehicle","Setting",0x401,1,0,0,0,0},
 {"volume","Setting",0x400,1,0,0,0,0},{"account","Setting",0x302,1,0,0,0,0},{"initial-setup","Setting",0x200,1,0,0,0,0},
 {"storage","DriveRec",0x601,2,0,0,0,0},{"alignment","DriveRec",0x701,1,0,0,0,0},
 {"recorder-front","Setting",0x600,1,2,1,1,2},{"recorder-cabin","Setting",0x600,1,3,2,1,2},
 {"recorder-audio","Setting",0x600,1,4,3,1,2},{"recorder-overlay","Setting",0x600,1,10,5,1,2},
 {"parking-security","Setting",0x600,6,8,2,1,2},{"parking-upload","Setting",0x600,6,9,3,1,3},
 {"recorder-impact","Setting",0x600,1,5,4,1,5},{"parking-impact","Setting",0x600,6,7,1,1,5},
 {"vehicle-type","Setting",0x401,1,2,1,1,6},{"collection-area","Setting",0x401,1,3,2,1,2},
 {"collection-direction","Setting",0x401,1,4,3,1,3},{"installation-direction","Setting",0x700,1,2,1,1,2},
 {"master-volume","Setting",0x400,1,2,1,0,10}
};
static const struct operation_spec*find_spec(const char*id){for(unsigned i=0;i<sizeof(specs)/sizeof(specs[0]);i++)if(same(id,specs[i].id))return &specs[i];return 0;}
static int number(const char*s,unsigned*value){if(!s||!s[0]||(s[0]=='0'&&s[1]))return 0;unsigned v=0;for(unsigned i=0;s[i];i++){if(i>=5||s[i]<'0'||s[i]>'9')return 0;v=v*10+(unsigned)(s[i]-'0');if(v>10000)return 0;}*value=v;return 1;}
static int card_matches(const char*expected){
 if(!expected||length(expected)!=64)return 0;unsigned count=0,matched=0;
 for(unsigned disk=0;disk<10;disk++){
  char base[]="/sys/class/block/mmcblk0",path[96];size_t n=length(base);base[n-1]=(char)('0'+disk);for(size_t i=0;i<n;i++)path[i]=base[i];const char*type="/device/type";for(size_t i=0;i<=length(type);i++)path[n+i]=type[i];u8 kind[8];int k=read_file(path,kind,sizeof(kind));if(k!=3||kind[0]!='S'||kind[1]!='D'||kind[2]!='\n')continue;count++;
  const char*cid="/device/cid";for(size_t i=0;i<=length(cid);i++)path[n+i]=cid[i];u8 identity[40];int id_size=read_file(path,identity,sizeof(identity));if(id_size!=33||identity[32]!='\n')return 0;
  const char*size="/size";for(size_t i=0;i<=length(size);i++)path[n+i]=size[i];u8 sectors[32];int sector_size=read_file(path,sectors,sizeof(sectors));if(sector_size<2||sectors[sector_size-1]!='\n')return 0;sector_size--;
  struct sha_state hash;u8 digest[32];sha_init(&hash);sha_update(&hash,(const u8*)base,n);sha_update(&hash,(const u8*)":",1);sha_update(&hash,identity,32);sha_update(&hash,(const u8*)":",1);sha_update(&hash,sectors,(size_t)sector_size);sha_final(&hash,digest);const char*hex="0123456789abcdef";unsigned equal=1;for(unsigned i=0;i<32;i++)if(expected[2*i]!=hex[digest[i]>>4]||expected[2*i+1]!=hex[digest[i]&15])equal=0;matched+=equal;
 }
 return count==1&&matched==1;
}
static int before_deadline(struct ncc_time*end){struct ncc_time now;if(clock_gettime(1,&now))fail("monotonic clock unavailable",78);return now.seconds<end->seconds||(now.seconds==end->seconds&&now.nanoseconds<end->nanoseconds);}
static unsigned exchange(common_send_fn send,queue_check_fn check,queue_receive_fn receive,void*destination,void*receiver,unsigned function,unsigned command,int has_value,unsigned wanted,unsigned*values){
 struct small_string payload;payload.pointer=payload.local;payload.size=0;payload.local[0]=0;
 if(has_value){payload.local[0]=8;payload.local[1]=(char)wanted;payload.size=2;payload.local[2]=0;}
 struct ncc_time deadline;if(clock_gettime(1,&deadline))fail("monotonic clock unavailable",78);deadline.seconds+=5;
 owner_live();unsigned nonce=++request_nonce;send(destination,0x4e43434f,nonce,(unsigned short)function,(unsigned short)command,&payload);
 while(before_deadline(&deadline)){if(check(receiver)){unsigned size=0;int rc=receive(receiver,response,sizeof(response),&size,0);if(rc==0){unsigned seen=0;if(ops_frame(response,size,owner_pid,current_owner,nonce,function,command|0x8000,values,&seen)){owner_live();return seen;}}else if(rc!=3)fail("own receiver read failed",78);}usleep(10000);}
 fail("correlated owner response timed out; no retry",78);return 0;
}
static void emit(const char*key,unsigned value){output(key);char digits[24];unsigned n=0;do{digits[n++]=(char)('0'+value%10);value/=10;}while(value);while(n)syscall(64,1,digits+--n,1);output("\n");}
static void action_exchange(const struct action_spec*a,common_send_fn send,queue_check_fn check,queue_receive_fn receive,void*destination,void*receiver){
 u8 input[2049];unsigned long count=0;for(;;){long n=syscall(63,0,input+count,sizeof(input)-count);if(n<0)fail("action input unavailable",64);if(!n)break;count+=(unsigned long)n;if(count==sizeof(input))fail("action input too large",64);}
 if(!action_input(a,input,count))fail("fixed action input invalid",64);
 struct small_string payload;payload.pointer=(char*)input;payload.size=count;struct ncc_time deadline;if(clock_gettime(1,&deadline))fail("monotonic clock unavailable",78);deadline.seconds+=a->seconds;
 owner_live();unsigned nonce=++request_nonce;send(destination,0x4e43434f,nonce,(unsigned short)a->function,(unsigned short)a->command,&payload);
 if(a->notify){output("sent=1\n");return;}
 while(before_deadline(&deadline)){if(check(receiver)){unsigned size=0;int rc=receive(receiver,response,sizeof(response),&size,0);if(rc==0){unsigned long start;if(ops_payload(response,size,owner_pid,current_owner,nonce,a->function,a->command|0x8000,&start)){
   output("payload=");const char*hex="0123456789abcdef";for(unsigned long i=start;i<size;i++){char pair[2]={hex[response[i]>>4],hex[response[i]&15]};syscall(64,1,pair,2);}output("\n");owner_live();return;
  }}else if(rc!=3)fail("own receiver read failed",78);}usleep(10000);}
 fail("correlated action response timed out; no retry",78);
}
int main(int argc,char**argv){
 (void)print_value;
 int get=argc==4&&same(argv[1],"--get"),set=argc==6&&same(argv[1],"--set"),format=argc==5&&same(argv[1],"--format")&&same(argv[3],"ERASE_SD");
 int card=argc==4&&same(argv[1],"--check-card");
 const struct action_spec*action=argc==4&&same(argv[1],"--action")?find_action(argv[3]):0;
 struct operation_spec action_owner={"action",action?action->owner:"",0,0,0,0,0,0};
 const struct operation_spec*spec=action?&action_owner:(get||set)?find_spec(argv[3]):(format||card)?find_spec("storage"):0;
 unsigned expected=0,next=0;if(!spec||(!get&&!set&&!format&&!card&&!action)||(set&&(!spec->set||!number(argv[4],&expected)||!number(argv[5],&next)||next<spec->min||next>spec->max)))fail("fixed operation and valid values required",64);
 current_owner=spec->owner;owner_pid=parse_pid(argv[2]);if(!owner_pid)fail("known owner PID required",64);
 preflight();const char*owner_hash=0;
 if(same(current_owner,"Setting")){current_path="/usr/local/bin/Setting";owner_hash="d5b876fd8e5a3c18037b25812f5d989351045ecd024b40a99c551c4d00f7e0ac";}
 else if(same(current_owner,"DriveRec")){current_path="/usr/local/bin/DriveRec";owner_hash="000871dc36581f27ae831d9db3772b553a17cce0d814c52867276ac68ea0cde0";}
 else if(same(current_owner,"System")){current_path="/usr/local/bin/System";owner_hash="6ff52824c541d187d1d6234c1f49eae32bf6eed3cb9d4e4744741810152e853a";}
 else if(same(current_owner,"Navigation")){current_path="/usr/local/bin/Navigation";owner_hash="692f35e7097ec07947a3a8e264a5275696ada5b2224ab36f5a440fe8befbcc21";}
 else if(same(current_owner,"ImageRecog")){current_path="/usr/local/bin/ImageRecog";owner_hash="02a37de0a0a246bd90612e8f4fea77d20aa9556b18259ffc1f9babd9805b4f47";}
 else if(same(current_owner,"SitAwrEng")){current_path="/usr/local/bin/SitAwrEng";owner_hash="9e126f737e274b36c6f3b558ef27b635082d30d701e9a8c176ad8b0aa465d206";}
 else if(same(current_owner,"Communication")){current_path="/usr/local/bin/Communication";owner_hash="aaa0d09d406a2b762e24b303bc138665d3da97523a07dcc338ce0ec4bec49c8b";}
 if(!owner_hash||!digest_file(current_path,owner_hash))fail("owner firmware mismatch",78);owner_live();
 if(card&&!card_matches(argv[3]))fail("card identity check failed",75);
 const char*enabled=getenv("NCC_NATIVE_OPERATIONS");if(!enabled||!same(enabled,"1"))fail("explicit operations launcher required",78);prepare_private_name();
 stage="pthread-bootstrap";prepare_vendor_phase();stage="NStandard-constructors";void*std=dlopen("/usr/local/lib/libNStandardLib.so",0x102);if(!std)loader_fail("NStandard load failed");
 real_memory_open=(void*(*)(void*,const unsigned short*))symbol(std,"_ZN17NProcSharedMemory4OpenEPKDs");real_memory_free=(long(*)(void*))symbol(std,"_ZN17NProcSharedMemory10FreeMemoryEv");real_memory_alloc=(void*(*)(void*,const unsigned short*,long))symbol(std,"_ZN17NProcSharedMemory5AllocEPKDsl");
 stage="AplLib-constructors";void*h=dlopen("/usr/local/lib/libAplLib.so",0x102);if(!h)loader_fail("AplLib load failed");
 typedef void(*ctor_fn)(void*);typedef void(*ext_ctor_fn)(void*,int,int);typedef void(*name_set_fn)(void*,const unsigned short*);typedef void*(*common_instance_fn)(void*);typedef int(*connect_fn)(void*,unsigned);
 ext_ctor_fn ext_ctor=(ext_ctor_fn)symbol(h,"_ZN22AIPI_ExternalInterfaceC1Eii");ctor_fn ext_dtor=(ctor_fn)symbol(h,"_ZN22AIPI_ExternalInterfaceD1Ev");ctor_fn initialize=(ctor_fn)symbol(h,"_ZN22AIPI_ExternalInterface10InitializeEv");ctor_fn name_ctor=(ctor_fn)symbol(h,"_ZN7NStringC1Ev");ctor_fn name_dtor=(ctor_fn)symbol(h,"_ZN7NStringD1Ev");name_set_fn name_set=(name_set_fn)symbol(h,"_ZN7NString3SetEPKDs");common_instance_fn instance=(common_instance_fn)symbol(h,"_ZN11AIPI_Common11GetInstanceERK7NString");connect_fn connect=(connect_fn)symbol(h,"_ZN9AIPI_Core7ConnectEj");common_send_fn send=(common_send_fn)symbol(h,"_ZN11AIPI_Common29RequestCommonFreeStyleCommandERK7NStringjjttRKNSt7__cxx1112basic_stringIcSt11char_traitsIcESaIcEEE");queue_check_fn check=(queue_check_fn)symbol(h,"_ZN16NProcMsgReceiver8CheckMsgEv");queue_receive_fn receive=(queue_receive_fn)symbol(h,"_ZN16NProcMsgReceiver10ReceiveMsgEPhjPjj");
 u8*base=(u8*)symbol(h,"_Z12std_lib_initv")-0x1ee28;u8*aipi=(u8*)(void*)send-0x2794c0;
 u8*apl=(u8*)symbol(h,"_ZN17AL_SettingService9GetStatusE13AL_SRV_SET_ID")-0xaa8a0;
 if(*(void**)(apl+NCC_AL_ENABLE_SLOT)!=(void*)require_initialized||*(void**)(base+NCC_STD_LOCK_SLOT)!=(void*)pthread_mutex_lock||*(void**)(base+NCC_STD_UNLOCK_SLOT)!=(void*)pthread_mutex_unlock||*(void**)(base+NCC_STD_TIMEDLOCK_SLOT)!=(void*)pthread_mutex_timedlock||*(void**)(base+NCC_STD_TRYLOCK_SLOT)!=(void*)pthread_mutex_trylock||*(void**)(base+NCC_STD_OPEN_SLOT)!=(void*)tracked_memory_open||*(void**)(base+NCC_STD_FREE_SLOT)!=(void*)tracked_memory_free||*(void**)(base+NCC_STD_CLEAN_SLOT)!=(void*)block_cleanup||*(void**)(base+NCC_STD_SEMOP_SLOT)!=(void*)semop||*(void**)(base+0x6d5f8)!=(void*)tracked_memory_alloc||*(void**)(aipi+0x4e1e88)!=(void*)tracked_memory_alloc||*(void**)(base+0x6cd08)!=(void*)open||*(void**)(base+0x6cbe8)!=(void*)fchown||*(void**)(base+0x6ca90)!=(void*)fchmod||*(void**)(base+0x6d200)!=(void*)ftruncate||*(void**)(base+0x6d0f0)!=(void*)unlink)fail("Operations process-local guard binding mismatch",78);
 struct{unsigned long object[512];unsigned long canary;}external;external.canary=0x4e43435641554431UL;unsigned long destination[4]={0};unsigned short owner_name[32]={0};for(unsigned i=0;current_owner[i];i++)owner_name[i]=(unsigned char)current_owner[i];
 stage="external-interface-constructor";ext_ctor(external.object,8192,8192);name_ctor(destination);name_set(destination,owner_name);stage="Setting-proxy-connect";void*proxy=instance(destination);if(!proxy||connect(proxy,100)!=0)fail("existing Setting IPC unavailable",78);
 stage="own-receiver-initialize";private_enabled=1;initialize(external.object);private_enabled=0;if(!private_created||external.canary!=0x4e43435641554431UL)fail("own receiver creation unconfirmed",78);


 struct ncc_time now;if(clock_gettime(1,&now))fail("monotonic clock unavailable",78);request_nonce=(unsigned)now.nanoseconds^(unsigned)getpid();
 if(action){action_exchange(action,send,check,receive,destination,(u8*)external.object+0x98);}else{
 unsigned values[8],seen=exchange(send,check,receive,destination,(u8*)external.object+0x98,spec->function,spec->get,0,0,values);
 if(set){if(values[spec->field]!=expected)fail("expected value conflict; no write attempted",75);unsigned original[8];for(unsigned i=0;i<8;i++)original[i]=values[i];unsigned result[8];exchange(send,check,receive,destination,(u8*)external.object+0x98,spec->function,spec->set,1,next,result);if(result[1]!=1)fail("owner rejected operation",78);
  seen=exchange(send,check,receive,destination,(u8*)external.object+0x98,spec->function,spec->get,0,0,values);if(values[spec->field]!=next)fail("setting read-back mismatch",78);for(unsigned i=1;i<8;i++)if(i!=spec->field&&original[i]!=values[i])fail("untargeted setting changed; no blind restore",75);
 }
 if(same(spec->id,"storage")){unsigned insertion=values[1];exchange(send,check,receive,destination,(u8*)external.object+0x98,0x601,4,0,0,values);unsigned status=values[1];
  if(format){if(insertion!=1||status!=2||!card_matches(argv[4]))fail("card changed, missing, busy or state unknown; no format attempted",75);unsigned result[8];exchange(send,check,receive,destination,(u8*)external.object+0x98,0x601,1,0,0,result);if(result[1]!=1)fail("DriveRec rejected format request",78);emit("accepted=",1);exchange(send,check,receive,destination,(u8*)external.object+0x98,0x601,4,0,0,values);status=values[1];}
  emit("inserted=",insertion);emit("formatStatus=",status);exchange(send,check,receive,destination,(u8*)external.object+0x98,0x601,3,0,0,values);emit("optionalCable=",values[1]);
 }else if(same(spec->id,"alignment")){emit("active=",values[1]);exchange(send,check,receive,destination,(u8*)external.object+0x98,0x701,10,0,0,values);emit("direction=",values[1]);exchange(send,check,receive,destination,(u8*)external.object+0x98,0x701,5,0,0,values);emit("angleOK=",values[1]);}
 else{emit("seen=",seen);for(unsigned i=1;i<8;i++){char key[8]={'f','i','e','l','d',(char)('0'+i),'=',0};emit(key,values[i]);}}
 }
 stage="own-receiver-peer-release";sole_receiver_reference();stage="own-receiver-destruction";name_dtor(destination);ext_dtor(external.object);release_owned_memories();if(ncc_owned_count||ncc_memory_count||!memory_refs_match())fail("audio cleanup reference mismatch",78);private_cleanup();if(private_created||external.canary!=0x4e43435641554431UL)fail("own receiver cleanup unconfirmed",78);owner_live();output("cleanup=matched; own-receiver-removed\n");syscall(94,0);for(;;){}
}
