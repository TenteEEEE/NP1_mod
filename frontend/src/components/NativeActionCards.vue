<script setup lang="ts">
import {computed,onUnmounted,ref,watch} from 'vue'
import {api} from '../services/api'
import {ArrowUpRight,Check,Settings2} from '@lucide/vue'
export type ActionInput={id:string;label:string;kind:'number'|'secret'|'text'|'ascii'|'optional-secret';min?:number;max?:number}
export type NativeAction={id:string;group:string;label:string;description:string;readOnly:boolean;confirmation?:string;unavailableReason?:string;inputs:ActionInput[]}
type Reply={values:Record<string,unknown>;readback?:Record<string,unknown>;sent:boolean;acknowledged:boolean;completionConfirmed:boolean;message:string}
const props=defineProps<{actions:NativeAction[];disabled:boolean}>()
const emit=defineEmits<{busy:[value:boolean]}>()
const groups=computed(()=>[...new Set(props.actions.filter(a=>a.id!=='alignment-keepalive').map(a=>a.group))])
const busy=ref(''),dialog=ref<NativeAction>(),fields=ref<Record<string,string>>({}),ack=ref(false),results=ref<Record<string,Reply>>({}),errors=ref<Record<string,string>>({}),alignment=ref(false)
const resetPhrase=ref('')
const base='/device/native-operations/actions/'
let alive=true,timer:ReturnType<typeof setTimeout>|undefined
watch(()=>!!busy.value||alignment.value,v=>emit('busy',v),{immediate:true})
const labels:Record<string,string>={result:'本体の処理結果',mode:'保守モード',learningState:'学習状態',antennaConnected:'アンテナ接続',antennaLevel:'受信レベル',positioningStatus:'測位状態',satellites:'受信衛星数',usedSatellites:'使用衛星数',mounted:'取付検出',trip:'センサートリップ',ssid:'SSID',password:'パスワード',security:'セキュリティ方式',band:'周波数帯',output:'シリアル出力',input:'コンソール入力',routeSynchronized:'ルート同期',ownerKey:'オーナーキー',status:'設定状態'}
function shown(key:string,value:unknown){if(key==='mode')return ['不明','OFF','Poseidon','APP','Sales','Service'][Number(value)]??String(value);if(key==='learningState')return ['不明','未学習','学習中','学習済み'][Number(value)]??String(value);if(key==='positioningStatus')return ['不明','未測位','1D','2D','3D','測位エラー'][Number(value)]??String(value);if(typeof value==='boolean')return value?'はい':'いいえ';if(value&&typeof value==='object')return JSON.stringify(value,null,2);return String(value)}
function open(action:NativeAction){if(props.disabled||busy.value||action.unavailableReason)return;if(action.readOnly&&!action.inputs.length){void execute(action);return}dialog.value=action;fields.value=Object.fromEntries(action.inputs.map(f=>[f.id,f.kind==='number'?String(f.min||0):'']));ack.value=false;resetPhrase.value=''}
const needsPhrase=computed(()=>dialog.value?.id==='factory-reset')
const valid=computed(()=>!!dialog.value&&(dialog.value.readOnly||ack.value)&&(!needsPhrase.value||resetPhrase.value==='本体を初期化')&&dialog.value.inputs.every(f=>{const value=fields.value[f.id]??'';if(f.kind==='number')return /^\d+$/.test(value)&&Number(value)>=(f.min||0)&&Number(value)<=(f.max??0xffffffff);return (value.length>0||f.kind==='optional-secret')&&new TextEncoder().encode(value).length<=(f.max||1024)}))
async function execute(action:NativeAction){
 if(props.disabled||busy.value)return
 const values=Object.fromEntries(action.inputs.map(f=>[f.id,f.kind==='number'?Number(fields.value[f.id]):fields.value[f.id]]))
 const body:{confirmation?:string;values:Record<string,unknown>;expectedValue?:boolean}={confirmation:action.confirmation,values}
 if(action.id.startsWith('serial-')&&action.id!=='serial-status'){
  const prior=results.value[action.group]?.readback??results.value[action.group]?.values,key=action.id.slice('serial-'.length)
  if(typeof prior?.[key]!=='boolean'){errors.value[action.group]='先にシリアルの状態を取得してください。';return}body.expectedValue=prior[key] as boolean
 }
 busy.value=action.id;delete errors.value[action.group];delete results.value[action.group];dialog.value=undefined;fields.value={};ack.value=false
 try{
  const readGet=action.readOnly&&!action.inputs.length
  const result=await api<Reply>(base+action.id,readGet?'GET':'POST',readGet?undefined:body)
  if(!result.sent||typeof result.acknowledged!=='boolean'||!result.values)throw new Error('本体の応答が不正です')
  if(!alive)return;results.value[action.group]=result
  if(action.id==='alignment-start'){alignment.value=true;scheduleKeepalive()}
  if(action.id==='alignment-cancel'||action.id==='alignment-finish'){alignment.value=false;if(timer)clearTimeout(timer)}
 }catch(e){if(alive)errors.value[action.group]=String(e)+' 操作を自動で再送しません。'}finally{busy.value=''}
}
function scheduleKeepalive(){timer=setTimeout(async()=>{
 if(!alive||!alignment.value)return
 const action=props.actions.find(a=>a.id==='alignment-keepalive');if(!action)return
 try{const result=await api<Reply>(base+action.id,'POST',{confirmation:action.confirmation,values:{}});if(result.values.result!==true)throw new Error('接続を維持できません')}catch{alignment.value=false;errors.value[action.group]='取付調整の接続確認に失敗しました。本体の状態を確認し、再度開始してください。';return}
 scheduleKeepalive()
 },7000)}
onUnmounted(()=>{alive=false;if(timer)clearTimeout(timer)})
</script>
<template>
 <section v-for="group in groups" :key="group" class="panel native-action-card" :aria-label="group+'の操作'">
  <div class="panel-head"><h2><Settings2 :size="18"/>{{group}}</h2></div>
  <div class="native-action-body">
   <p v-if="group==='取付の調整'&&alignment" class="action-notice" role="status">取付調整中です。この画面を開いたまま、停車して調整してください。画面を閉じると接続維持が止まります。</p>
   <a v-if="group==='取付の調整'" href="#camera" target="_blank" rel="noopener noreferrer" class="action-camera">別タブでライブ映像を確認<ArrowUpRight :size="14"/></a>
   <div v-for="action in actions.filter(a=>a.group===group&&a.id!=='alignment-keepalive')" :key="action.id" class="native-action-item"><p>{{action.description}}</p><p v-if="action.unavailableReason" class="action-unavailable">{{action.unavailableReason}}</p><button :class="action.readOnly?'secondary':'primary'" :disabled="disabled||!!busy||!!action.unavailableReason" @click="open(action)">{{busy===action.id?'本体に確認中…':action.label}}</button></div>
   <p v-if="errors[group]" role="alert" class="action-error">{{errors[group]}}</p>
   <div v-if="results[group]" class="action-result" role="status"><p><Check :size="15"/>{{results[group]!.message}}</p><p v-if="!results[group]!.completionConfirmed" class="muted">要求の送信・応答と、動作の完了は別です。本体の案内や状態を確認してください。</p><dl><template v-for="(value,key) in results[group]!.values" :key="key"><dt>{{labels[key]||key}}</dt><dd>{{shown(key,value)}}</dd></template></dl><template v-if="results[group]!.readback"><h3>本体から再取得</h3><dl><template v-for="(value,key) in results[group]!.readback" :key="key"><dt>{{labels[key]||key}}</dt><dd>{{shown(key,value)}}</dd></template></dl></template><button class="text-button" @click="delete results[group]">結果を閉じる</button></div>
  </div>
 </section>
 <Teleport to="body"><div v-if="dialog" class="modal-backdrop" @click.self="dialog=undefined"><section class="modal action-modal" role="dialog" aria-modal="true" aria-labelledby="native-action-title"><h2 id="native-action-title">{{dialog.label}}</h2><p>{{dialog.description}}</p><form @submit.prevent="valid&&execute(dialog!)"><label v-for="field in dialog.inputs" :key="field.id" class="action-input">{{field.label}}<input v-model="fields[field.id]" :type="field.kind.includes('secret')?'password':field.kind==='number'?'number':'text'" :min="field.min||0" :max="field.max" :maxlength="field.kind==='number'?undefined:field.max" autocomplete="off" :spellcheck="false" :required="field.kind!=='optional-secret'"></label><label v-if="needsPhrase" class="action-input">確認のため「本体を初期化」と入力してください<input v-model="resetPhrase" autocomplete="off"></label><label v-if="!dialog.readOnly" class="action-ack"><input v-model="ack" type="checkbox">操作内容と本体への影響を確認しました</label><div class="modal-actions"><button type="button" class="secondary" @click="dialog=undefined">キャンセル</button><button type="submit" class="primary" :disabled="!valid||disabled||!!busy">{{dialog.label}}</button></div></form></section></div></Teleport>
</template>
<style scoped>
.native-action-card{min-width:0}.panel-head h2{display:flex;gap:9px;align-items:center}.native-action-body{padding:18px 22px}.native-action-item{display:grid;gap:10px;padding:15px 0;border-bottom:1px solid var(--line)}.native-action-item:first-child{padding-top:0}.native-action-item p,.action-notice,.action-error,.action-modal>p{font-size:12px;line-height:1.8;color:var(--muted)}.native-action-item button{min-height:44px;width:100%;white-space:normal;font-size:12px}.action-notice{padding:12px;background:var(--accent-soft);border-radius:10px}.action-camera{display:flex;gap:5px;align-items:center;font-size:12px;margin-bottom:12px;color:var(--accent)}.action-error{color:var(--danger);margin-top:12px;overflow-wrap:anywhere}.action-result{margin-top:16px;padding:14px;background:var(--bg);border:1px solid var(--line);border-radius:12px;overflow-wrap:anywhere}.action-result>p{display:flex;align-items:flex-start;gap:6px;font-size:12px;line-height:1.8}.action-result>p svg{flex:none;margin-top:3px}.action-result dl{display:grid;grid-template-columns:minmax(90px,1fr) minmax(0,1fr);gap:8px;font-size:12px;margin-block:12px}.action-result dd{margin:0;word-break:break-all}.action-result h3{font-size:12px;margin-top:12px}.action-modal{max-height:calc(100dvh - 40px);overflow-y:auto}.action-modal h2{font-size:18px;margin-bottom:12px}.action-input{display:grid;gap:8px;margin-top:16px;font-size:13px}.action-input input{width:100%;min-width:0}.action-ack{display:flex;align-items:flex-start;gap:10px;margin-top:20px;font-size:13px;line-height:1.8}.action-ack input{width:18px;height:18px;flex:none;margin-top:3px}.action-modal .modal-actions{display:flex;justify-content:flex-end;flex-wrap:wrap;gap:10px;margin-top:22px}.action-modal button{min-height:44px;white-space:normal}
</style>
