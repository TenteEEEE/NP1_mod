const fs=require('node:fs'),assert=require('node:assert/strict');
(async()=>{
 const {instance}=await WebAssembly.instantiate(fs.readFileSync(process.argv[2])),e=instance.exports;
 const frame=(payload=Buffer.from([8,1]))=>{const b=Buffer.alloc(56+payload.length);b.writeBigUInt64LE(0x80001f02n);b.writeUInt32LE(321,8);b.writeUInt32LE(9,14);b.write('DriveRec\0',18,'utf16le');b.writeUInt32LE(0x4e43434f,36);b.writeUInt32LE(42,40);b.writeUInt16LE(0x601,44);b.writeUInt16LE(0x8002,46);b.writeBigUInt64LE(BigInt(payload.length),48);payload.copy(b,56);return b;};
 const parse=b=>{new Uint8Array(e.memory.buffer,e.ncc_ops_buffer(),b.length).set(b);return e.ncc_ops_parse(b.length,321,42,0x601,0x8002);};
 assert.equal(parse(frame()),1);assert.equal(e.ncc_ops_value(1),1);assert.equal(e.ncc_ops_seen(),2);
 assert.equal(parse(frame(Buffer.alloc(0))),1);assert.equal(e.ncc_ops_value(1),0);
 for(const position of [0,8,14,18,36,40,44,46,48]){const b=frame();b[position]^=1;assert.equal(parse(b),0,'uncorrelated frame '+position);}
 for(const payload of [[8],[8,1,8,0],[10,0],[8,128,0],[64,1],[8,255,255,255,255,31]])assert.equal(parse(frame(Buffer.from(payload))),0);
 assert.equal(parse(frame(Buffer.from([8,255,255,255,255,255,255,255,255,255,1]))),1);assert.equal(e.ncc_ops_value(1)>>>0,0xffffffff);
 for(let n=0;n<58;n++)assert.equal(parse(frame().subarray(0,n)),0);
 const raw=frame(Buffer.from([10,5,104,101,108,108,111]));new Uint8Array(e.memory.buffer,e.ncc_ops_buffer(),raw.length).set(raw);assert.equal(e.ncc_ops_raw(raw.length),1);raw[40]^=1;new Uint8Array(e.memory.buffer,e.ncc_ops_buffer(),raw.length).set(raw);assert.equal(e.ncc_ops_raw(raw.length),0);
 const names=[...fs.readFileSync(require('node:path').join(__dirname,'operations-actions.h'),'utf8').matchAll(/\{"([a-z-]+)","[A-Za-z]+",0x/g)].map(m=>m[1]);assert.equal(names.length,e.ncc_ops_action_count());
 const validate=(name,payload)=>{const b=Buffer.from(payload);new Uint8Array(e.memory.buffer,e.ncc_ops_buffer(),b.length).set(b);return e.ncc_ops_action(names.indexOf(name),b.length)};
 assert.equal(validate('navigation-start',[8,255,255,255,255,15]),1);assert.equal(validate('serial-input',[8,0]),1);assert.equal(validate('serial-input',[8,2]),0);assert.equal(validate('license-export',[8,1]),0);
 assert.equal(validate('alignment-camera',[8,2]),1);assert.equal(validate('alignment-camera',[8,3]),0);assert.equal(validate('account-login',[18,1,65,26,1,66]),1);assert.equal(validate('account-login',[18,1,65]),0);assert.equal(validate('maintenance-verify',[10,1,65,10,1,66]),0);
 assert.equal(validate('wifi-delete',[10,1,65,16,2]),1);assert.equal(validate('wifi-delete',[10,1,0,16,2]),0);assert.equal(validate('wifi-connect',[10,1,65,16,1,26,0]),1);assert.equal(validate('local-ap-settings',[10,1,65,18,0,24,1,32,1]),1);
 console.log('PASS: operations frame correlation, proto3 defaults, duplicate/truncated/malformed fields and signed int32');
 console.log('PASS: raw response correlation and fixed action payload allowlist');
})();
