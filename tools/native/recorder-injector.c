/* Pinned AArch64 DriveRec runtime loader. It changes only the selected
 * process memory and restores every register and stack byte before detach. */
#include "native-core.h"

typedef long ssize_t;
typedef long pid_t;
struct ncc_iovec {void*base;size_t length;};
struct ncc_dl_info {const char*file;void*base;const char*symbol;void*symbol_address;};
struct user_pt_regs {unsigned long regs[31];unsigned long sp;unsigned long pc;unsigned long pstate;};
extern int open(const char*,int,...);
extern int close(int);
extern ssize_t read(int,void*,size_t);
extern long ptrace(int,pid_t,void*,void*);
extern pid_t waitpid(pid_t,int*,int);
extern void*dlopen(const char*,int);
extern char*dlerror(void);
extern int dladdr(const void*,struct ncc_dl_info*);
extern void*memcpy(void*,const void*,size_t);
extern size_t strlen(const char*);
extern int snprintf(char*,size_t,const char*,...);
extern void _exit(int);
typedef struct ncc_dir DIR;
struct ncc_dirent {unsigned long ino;long offset;unsigned short record_length;unsigned char type;char name[256];};
extern DIR*opendir(const char*);
extern struct ncc_dirent*readdir(DIR*);
extern int closedir(DIR*);

#define O_RDONLY 0
#define O_CLOEXEC 02000000
#define PTRACE_PEEKDATA 2
#define PTRACE_POKEDATA 5
#define PTRACE_CONT 7
#define PTRACE_ATTACH 16
#define PTRACE_DETACH 17
#define PTRACE_GETREGSET 0x4204
#define PTRACE_SETREGSET 0x4205
#define NT_PRSTATUS 1
#define WAIT_ALL_TRACED 0x40000000
#define DRIVE_REC_SHA "000871dc36581f27ae831d9db3772b553a17cce0d814c52867276ac68ea0cde0"

static void out(const char*s){size_t n=0;while(s[n])n++;extern ssize_t write(int,const void*,size_t);(void)write(1,s,n);}
static void fail(const char*s,int code){out("error=");out(s);out("\n");_exit(code);}
static char maps_data[262144];
static int digest_file(const char*path,const char*expected){
 int fd=open(path,O_RDONLY|O_CLOEXEC);if(fd<0)return 0;struct sha_state state;sha_init(&state);u8 buffer[4096],digest[32];unsigned long total=0;
 for(;;){ssize_t n=read(fd,buffer,sizeof(buffer));if(n<0||total>64UL*1024*1024){close(fd);return 0;}if(!n)break;total+=(unsigned long)n;sha_update(&state,buffer,(size_t)n);}close(fd);sha_final(&state,digest);
 const char*hex="0123456789abcdef";for(int i=0;i<32;i++)if(expected[2*i]!=hex[digest[i]>>4]||expected[2*i+1]!=hex[digest[i]&15])return 0;return 1;
}
static unsigned parse_pid(const char*s){unsigned v=0;if(!s||!s[0])return 0;for(size_t i=0;s[i];i++){if(i>=10||s[i]<'0'||s[i]>'9'||v>214748364u)return 0;v=v*10+(unsigned)(s[i]-'0');if(v>2147483647u)return 0;}return v;}
static unsigned long mapping_base(unsigned pid,const char*library){
 char path[64];if(snprintf(path,sizeof(path),"/proc/%u/maps",pid)<=0)return 0;int fd=open(path,O_RDONLY|O_CLOEXEC);if(fd<0)return 0;size_t used=0;while(used+1<sizeof(maps_data)){ssize_t got=read(fd,maps_data+used,sizeof(maps_data)-used-1);if(got<0){close(fd);return 0;}if(!got)break;used+=(size_t)got;}close(fd);if(!used||used+1>=sizeof(maps_data))return 0;maps_data[used]=0;
 for(char*line=maps_data;*line;){char*end=line;while(*end&&*end!='\n')end++;char saved=*end;*end=0;unsigned long start=0,offset=1;char name[256]={0};extern int sscanf(const char*,const char*,...);if(sscanf(line,"%lx-%*lx %*4s %lx %*s %*s %255s",&start,&offset,name)==3&&offset==0&&same(name,library))return start;*end=saved;line=saved?end+1:end;}return 0;
}
static int wait_stopped(unsigned pid,int expected){int status=0;if(waitpid((pid_t)pid,&status,WAIT_ALL_TRACED)!=(pid_t)pid)return 0;return (status&0xff)==0x7f&&((status>>8)&0xff)==expected;}
static int get_regs(unsigned pid,struct user_pt_regs*regs){struct ncc_iovec io={regs,sizeof(*regs)};return ptrace(PTRACE_GETREGSET,(pid_t)pid,(void*)NT_PRSTATUS,&io)==0&&io.length==sizeof(*regs);}
static int set_regs(unsigned pid,struct user_pt_regs*regs){struct ncc_iovec io={regs,sizeof(*regs)};return ptrace(PTRACE_SETREGSET,(pid_t)pid,(void*)NT_PRSTATUS,&io)==0;}
static void remote_text(unsigned pid,unsigned long address,char*out_text,size_t capacity){size_t used=0;if(!capacity)return;while(used+1<capacity){unsigned long word=(unsigned long)ptrace(PTRACE_PEEKDATA,(pid_t)pid,(void*)address,0);for(unsigned i=0;i<8&&used+1<capacity;i++){char value=(char)(word>>(i*8));if(!value){out_text[used]=0;return;}out_text[used++]=(value>=32&&value<=126)?value:'?';}address+=8;}out_text[used]=0;}
static unsigned frozen_threads[256],frozen_count;
static int contains_thread(unsigned tid){for(unsigned i=0;i<frozen_count;i++)if(frozen_threads[i]==tid)return 1;return 0;}
static void thaw_threads(void){while(frozen_count){unsigned tid=frozen_threads[--frozen_count];(void)ptrace(PTRACE_DETACH,(pid_t)tid,0,0);}}
static int freeze_workers(unsigned pid){
 char path[64];if(snprintf(path,sizeof(path),"/proc/%u/task",pid)<=0)return 0;
 for(unsigned pass=0;pass<3;pass++){DIR*directory=opendir(path);if(!directory)return 0;int added=0;struct ncc_dirent*entry;
  while((entry=readdir(directory))){unsigned tid=parse_pid(entry->name);if(!tid||tid==pid||contains_thread(tid))continue;if(frozen_count>=256){closedir(directory);return 0;}if(ptrace(PTRACE_ATTACH,(pid_t)tid,0,0)||!wait_stopped(tid,19)){(void)ptrace(PTRACE_DETACH,(pid_t)tid,0,0);closedir(directory);return 0;}frozen_threads[frozen_count++]=tid;added=1;}
  if(closedir(directory))return 0;if(!added)return 1;
 }
 return 0;
}
int main(int argc,char**argv){
 if(argc!=3)fail("usage",64);unsigned pid=parse_pid(argv[1]);const char*hook=argv[2];if(pid<=1||strlen(hook)>=240||hook[0]!='/')fail("arguments",64);
 char exe[64];if(snprintf(exe,sizeof(exe),"/proc/%u/exe",pid)<=0||!digest_file(exe,DRIVE_REC_SHA))fail("DriveRec identity mismatch",75);if(!digest_file(hook,"@HOOK_SHA@"))fail("hook identity mismatch",75);
 struct ncc_dl_info local={0};if(!dladdr((void*)dlopen,&local)||!local.file||!local.base)fail("local loader identity unavailable",78);unsigned long remote_base=mapping_base(pid,"/lib/libdl-2.28.so");if(!remote_base)fail("remote loader mapping unavailable",78);unsigned long remote_dlopen=remote_base+((unsigned long)(void*)dlopen-(unsigned long)local.base),remote_dlerror=remote_base+((unsigned long)(void*)dlerror-(unsigned long)local.base);
 if(ptrace(PTRACE_ATTACH,(pid_t)pid,0,0)||!wait_stopped(pid,19))fail("attach failed",78);if(!freeze_workers(pid)){thaw_threads();ptrace(PTRACE_DETACH,(pid_t)pid,0,0);fail("thread freeze failed",78);}struct user_pt_regs saved,current;if(!get_regs(pid,&saved)){thaw_threads();ptrace(PTRACE_DETACH,(pid_t)pid,0,0);fail("register read failed",78);}current=saved;
 unsigned long remote_path=(saved.sp+512UL)&~15UL;unsigned long backup[32],words[32];for(unsigned i=0;i<32;i++){backup[i]=(unsigned long)ptrace(PTRACE_PEEKDATA,(pid_t)pid,(void*)(remote_path+i*8),0);words[i]=0;}memcpy(words,hook,strlen(hook)+1);
 for(unsigned i=0;i<32;i++)if(ptrace(PTRACE_POKEDATA,(pid_t)pid,(void*)(remote_path+i*8),(void*)words[i])){for(unsigned j=0;j<i;j++)ptrace(PTRACE_POKEDATA,(pid_t)pid,(void*)(remote_path+j*8),(void*)backup[j]);thaw_threads();ptrace(PTRACE_DETACH,(pid_t)pid,0,0);fail("stack write failed",78);}
 current.regs[0]=remote_path;current.regs[1]=0x102;current.regs[30]=0;current.pc=remote_dlopen;if(!set_regs(pid,&current)||ptrace(PTRACE_CONT,(pid_t)pid,0,0)||!wait_stopped(pid,11)){for(unsigned i=0;i<32;i++)ptrace(PTRACE_POKEDATA,(pid_t)pid,(void*)(remote_path+i*8),(void*)backup[i]);set_regs(pid,&saved);thaw_threads();ptrace(PTRACE_DETACH,(pid_t)pid,0,0);fail("remote dlopen did not return",78);}
 if(!get_regs(pid,&current)){for(unsigned i=0;i<32;i++)ptrace(PTRACE_POKEDATA,(pid_t)pid,(void*)(remote_path+i*8),(void*)backup[i]);set_regs(pid,&saved);thaw_threads();ptrace(PTRACE_DETACH,(pid_t)pid,0,0);fail("remote register result unavailable",78);}
 if(current.pc!=0){for(unsigned i=0;i<32;i++)ptrace(PTRACE_POKEDATA,(pid_t)pid,(void*)(remote_path+i*8),(void*)backup[i]);set_regs(pid,&saved);thaw_threads();ptrace(PTRACE_DETACH,(pid_t)pid,0,0);fail("remote dlopen faulted before return",78);}
 if(!current.regs[0]){current=saved;current.regs[30]=0;current.pc=remote_dlerror;char detail[192]={0};if(set_regs(pid,&current)&&!ptrace(PTRACE_CONT,(pid_t)pid,0,0)&&wait_stopped(pid,11)&&get_regs(pid,&current)&&current.pc==0&&current.regs[0])remote_text(pid,current.regs[0],detail,sizeof(detail));for(unsigned i=0;i<32;i++)ptrace(PTRACE_POKEDATA,(pid_t)pid,(void*)(remote_path+i*8),(void*)backup[i]);set_regs(pid,&saved);thaw_threads();ptrace(PTRACE_DETACH,(pid_t)pid,0,0);if(detail[0]){out("error=remote dlopen returned null: ");out(detail);out("\n");_exit(78);}fail("remote dlopen returned null",78);}
 unsigned long remote_hook=mapping_base(pid,hook);int stack_restored=1;for(unsigned i=0;i<32;i++)if(ptrace(PTRACE_POKEDATA,(pid_t)pid,(void*)(remote_path+i*8),(void*)backup[i]))stack_restored=0;if(!stack_restored||!remote_hook){set_regs(pid,&saved);thaw_threads();ptrace(PTRACE_DETACH,(pid_t)pid,0,0);fail(!stack_restored?"stack restore failed":"remote hook mapping unavailable",78);}
 thaw_threads();current=saved;current.regs[30]=0;current.pc=remote_hook+@REAPPLY_OFFSET@;int reapply_ok=set_regs(pid,&current)&&!ptrace(PTRACE_CONT,(pid_t)pid,0,0)&&wait_stopped(pid,11)&&get_regs(pid,&current)&&current.pc==0&&current.regs[0]==1;
 int registers_restored=set_regs(pid,&saved);int detached=ptrace(PTRACE_DETACH,(pid_t)pid,0,0)==0;if(!registers_restored||!detached)fail("detach restore failed",78);if(!reapply_ok)fail("runtime reapply was not scheduled",78);out("injected=1\nreapply=scheduled\nregisters=restored\nstack=restored\n");return 0;
}
