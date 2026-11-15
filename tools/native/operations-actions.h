#ifndef NCC_OPERATIONS_ACTIONS_H
#define NCC_OPERATIONS_ACTIONS_H
/* Fixed request allowlist. No caller-selected function, opcode, owner or path.
 * Input kind: 0 empty, 1 bool, 2 uint32, 3 password string, 4 login, 5 check-in.
 * Notification-only requests never claim an owner acknowledgement. */
struct action_spec{const char*id;const char*owner;unsigned function,command,kind,notify,seconds;};
static const struct action_spec actions[]={
 {"alignment-start","DriveRec",0x701,2,0,0,5},
 {"alignment-cancel","DriveRec",0x701,3,0,1,5},
 {"alignment-finish","DriveRec",0x701,4,0,1,5},
 {"alignment-keepalive","DriveRec",0x701,6,0,0,5},
 {"alignment-camera","DriveRec",0x701,11,2,0,5},
 {"alignment-ap","DriveRec",0x701,12,0,0,5},
 {"license-export","System",0x304,1,0,0,20},
 {"maintenance-status","System",0xe000,5,0,0,5},
 {"maintenance-start","System",0xe000,1,0,1,5},
 {"maintenance-cancel","System",0xe000,3,0,1,5},
 {"maintenance-verify","System",0xe000,4,3,0,5},
 {"diagnostic-export","System",0xf00b,1,0,0,20},
 {"serial-status","System",0xf00d,1,0,0,5},
 {"serial-output","System",0xf00d,2,1,0,5},
 {"serial-input","System",0xf00d,3,1,0,5},
 {"navigation-start","Navigation",0x800,1,2,0,5},
 {"navigation-update","Navigation",0x800,2,2,0,5},
 {"navigation-stop","Navigation",0x800,3,0,0,5},
 {"navigation-road","Navigation",0x800,4,2,0,5},
 {"rear-window-reset","ImageRecog",0xa01,1,0,0,5},
 {"rear-window-status","ImageRecog",0xa01,2,0,0,5},
 {"gnss-info","SitAwrEng",0x300,1,0,0,5},
 {"sensor-trip","SitAwrEng",0x301,3,0,0,5},
 {"sensor-clear","SitAwrEng",0x301,1,0,0,5},
 {"sensor-trip-reset","SitAwrEng",0x301,2,0,0,5},
 {"account-login","Setting",0x302,2,4,0,5},
 {"account-checkin","Setting",0x302,3,5,0,5},
 {"vehicle-setup-status","Setting",0x200,3,0,0,5},
 {"vehicle-setup-finish","Setting",0x200,4,0,1,5},
 {"initial-setup-finish","Setting",0x200,2,2,1,5},
 {"owner-key","Setting",0x200,6,0,0,5}
 ,{"factory-reset","Setting",0x200,5,0,1,5}
 ,{"wifi-status","Communication",0x500,1,0,0,5}
 ,{"wifi-switch","Communication",0x500,2,2,0,5}
 ,{"wifi-profile","Communication",0x500,3,6,0,5}
 ,{"wifi-saved","Communication",0x500,4,2,0,5}
 ,{"wifi-nearby","Communication",0x500,5,2,0,15}
 ,{"wifi-connecting","Communication",0x500,6,0,0,5}
 ,{"wifi-delete","Communication",0x500,7,6,0,5}
 ,{"wifi-autoconnect","Communication",0x500,8,9,0,5}
 ,{"wifi-connect","Communication",0x500,9,7,0,15}
 ,{"wifi-disconnect","Communication",0x500,10,0,0,5}
 ,{"wifi-reset","Communication",0x500,11,0,0,5}
 ,{"wifi-connect-saved","Communication",0x500,12,6,0,15}
 ,{"local-ap-status","Communication",0x501,1,0,0,5}
 ,{"local-ap-switch","Communication",0x501,2,2,0,5}
 ,{"local-ap-vehicle","Communication",0x501,3,0,0,5}
 ,{"local-ap-settings","Communication",0x501,4,8,0,5}
 ,{"local-ap-support","Communication",0x501,5,0,0,5}
 ,{"local-ap-reset","Communication",0x501,6,0,0,5}
};
static const struct action_spec*find_action(const char*id){for(unsigned i=0;i<sizeof(actions)/sizeof(actions[0]);i++)if(same(id,actions[i].id))return &actions[i];return 0;}
static int action_input(const struct action_spec*a,const unsigned char*b,unsigned long n){
 if(n>2048)return 0;if(!a->kind)return n==0;
 if(a->kind>=6){unsigned long p=0;unsigned mask=0;while(p<n){unsigned key,v;if(!ops_varint(b,n,&p,&key))return 0;unsigned f=key>>3,w=key&7;if(!f||f>4||(mask&(1u<<f)))return 0;mask|=1u<<f;
   int text=f==1||(a->kind==7&&f==3)||(a->kind==8&&f==2);
   if(text){if(w!=2||!ops_varint(b,n,&p,&v)||v>128||v>n-p||(f==1&&(!v||v>32)))return 0;for(unsigned i=0;i<v;i++)if(b[p+i]<32||b[p+i]>126)return 0;p+=v;}
   else{if(w||!ops_varint(b,n,&p,&v)||v<1||v>3||(((a->kind==8&&f==4)||(a->kind==9&&f==3))&&v>2))return 0;}
  }return mask==(a->kind==6?6:a->kind==8?30:14);
 }
 unsigned long p=0;unsigned seen=0;
 while(p<n){unsigned key,value;if(!ops_varint(b,n,&p,&key))return 0;unsigned field=key>>3,wire=key&7;if(!field||field>5||(seen&(1u<<field)))return 0;seen|=1u<<field;
  if(a->kind<=2){if(field!=1||wire||!ops_varint(b,n,&p,&value)||(a->kind==1&&value>1))return 0;
   if(same(a->id,"alignment-camera")&&(value<1||value>2))return 0;
   if(same(a->id,"initial-setup-finish")&&(value<1||value>3))return 0;
  }else{if(wire!=2||!ops_varint(b,n,&p,&value)||!value||value>1024||value>n-p)return 0;
   if((a->kind==3&&field!=1)||(a->kind==4&&(field<2||field>3))||(a->kind==5&&(field<2||field>5)))return 0;
   for(unsigned i=0;i<value;i++)if(!b[p+i])return 0;p+=value;
  }
 }
 return a->kind<=3?seen==2:a->kind==4?seen==12:seen==60;
}
#endif
