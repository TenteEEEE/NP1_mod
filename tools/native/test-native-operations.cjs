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
 console.log('PASS: operations frame correlation, proto3 defaults, duplicate/truncated/malformed fields and signed int32');
})();
