<script setup lang="ts">
import {computed,nextTick,onBeforeUpdate,onMounted,onUnmounted,ref,watch} from 'vue'
import {AlertTriangle,ArrowUpRight,HardDrive,RefreshCw,Smartphone} from '@lucide/vue'
import {api,bytes} from '../services/api'
import {nativeAppFeatures} from '../services/native-app'
import NativeAudioSettings from './NativeAudioSettings.vue'
import RecorderAdvancedSettings from './RecorderAdvancedSettings.vue'
import NativeActionCards,{type NativeAction} from './NativeActionCards.vue'
type Item={id:string;label:string;field:string;writable:boolean;options?:{value:number;label:string}[]}
type Group={id:string;label:string;items:Item[]}
type Values=Record<string,number>
type State={values:Values;observedAt:string;readback?:boolean}
type Preview={token:string;expiresAt:string;capacityBytes:number}
const props=withDefaults(defineProps<{mode?:'all'|'recorder'|'audio';content?:'all'|'settings'|'actions'|'features'}>(),{mode:'all',content:'all'})
const groups=ref<Group[]>([]),states=ref<Record<string,State>>({}),drafts=ref<Record<string,number>>({}),errors=ref<Record<string,string>>({})
const loading=ref(false),busy=ref(''),available=ref(false),notice=ref(''),error=ref(''),preview=ref<Preview>(),confirmed=ref(false),phrase=ref(''),checking=ref(false),pending=ref(false)
const ready=ref(false)
const audioBusy=ref(false)
const actions=ref<NativeAction[]>([]),actionBusy=ref(false)
const now=ref(Date.now())
let alive=true,poll:ReturnType<typeof setTimeout>|undefined,clock:ReturnType<typeof setInterval>|undefined,layoutFrame=0,operationResizeObserver:ResizeObserver|undefined,operationCards:HTMLElement[]=[]
const base='/device/native-operations'
const scoped=computed(()=>props.mode!=='all')
const showSettings=computed(()=>scoped.value||props.content==='all'||props.content==='settings')
const showActions=computed(()=>props.mode==='all'&&(props.content==='all'||props.content==='actions'))
const showFeatures=computed(()=>props.mode==='all'&&(props.content==='all'||props.content==='features'))
const title=computed(()=>props.mode==='recorder'?'録画設定':props.mode==='audio'?'本体の音声':props.content==='settings'?'本体の基本設定':props.content==='actions'?'操作・診断':props.content==='features'?'関連機能':'本体の操作')
const description=computed(()=>props.mode==='recorder'?'通常録画・駐車録画・H.264の詳細をまとめて設定します。':props.mode==='audio'?'本体の音量・消音・音量補正をまとめて設定します。':props.content==='settings'?'本体、車両、取付状態の基本項目を確認・変更します。':props.content==='actions'?'取付調整、接続、診断、保守の操作を用途別に実行します。':props.content==='features'?'NP1 Control CenterとMy NP1で利用できる機能を確認します。':'取付・接続・診断など、NP1本体の機能を操作します。')
const refreshLabel=computed(()=>props.mode==='recorder'?'録画設定を再取得':props.mode==='audio'?'音声設定を再取得':props.content==='actions'?'操作情報を再取得':'本体の状態を更新')
const storage=computed(()=>states.value.storage?.values)
const statusLabel=computed(()=>!storage.value?'未取得':storage.value.formatStatus===1?'フォーマット中':storage.value.formatStatus===2?'処理中ではありません':'状態不明')
const canFormat=computed(()=>available.value&&!loading.value&&!busy.value&&!audioBusy.value&&!actionBusy.value&&storage.value?.inserted===1&&storage.value.formatStatus===2&&!pending.value)
const canExecute=computed(()=>preview.value&&confirmed.value&&phrase.value==='SDカードを消去'&&Date.parse(preview.value.expiresAt)>now.value&&!busy.value)
const message=(e:unknown)=>e instanceof Error?e.message:String(e)
function show(item:Item,group:string){const value=states.value[group]?.values[item.field];if(value===undefined)return '未取得';if(value>=0x80000000)return '未設定';return item.options?.find(o=>o.value===value)?.label??String(value)}
function writable(item:Item,group:string){const value=states.value[group]?.values[item.field];return available.value&&item.writable&&value!==undefined&&item.options?.some(o=>o.value===value)&&!busy.value&&!loading.value&&!audioBusy.value&&!actionBusy.value}
function receive(group:string,state:State){if(!state.values||!state.observedAt)throw new Error('本体の応答が不正です');states.value[group]=state;for(const item of groups.value.find(g=>g.id===group)?.items||[])drafts.value[item.id]=state.values[item.field]!;delete errors.value[group]}
async function loadGroup(group:string){
 // Only read requests may wait for another native operation. Mutations are
 // never replayed after a timeout or other failure.
 for(let attempt=0;alive&&attempt<20;attempt++){
  try{const state=await api<State>(base+'/state/'+group);if(alive)receive(group,state);return}
  catch(e){if(message(e).startsWith('409:')&&attempt<19){await new Promise(resolve=>setTimeout(resolve,300));continue}if(alive){delete states.value[group];errors.value[group]=message(e)}return}
 }
}
async function load(){
 if(loading.value||busy.value||actionBusy.value||audioBusy.value)return
 loading.value=true;error.value=''
 try{
  if(props.mode==='all'&&props.content==='features'){
   available.value=true
   groups.value=[]
   actions.value=[]
   return
  }
  const catalog=await api<{groups:Group[];actions?:NativeAction[];available:boolean}>(base)
  if(!alive)return
  groups.value=(catalog.groups||[]).filter(group=>props.mode==='recorder'?['recorder','parking'].includes(group.id):props.mode==='audio'?group.id==='volume':showSettings.value&&!['recorder','parking','volume'].includes(group.id))
  actions.value=showActions.value?catalog.actions||[]:[]
  available.value=catalog.available===true&&(!scoped.value||groups.value.length>0)
  if(!available.value){error.value=props.mode==='recorder'?'本体の録画設定を利用できません。':props.mode==='audio'?'本体の音声設定を利用できません。':'本体操作のサービスを利用できません。';return}
  if(props.mode==='all'&&showSettings.value)await loadGroup('storage')
  for(const group of groups.value){if(!alive)break;await loadGroup(group.id)}
 }catch(e){error.value=message(e);available.value=false;states.value={}}finally{loading.value=false;ready.value=true}
}
async function apply(group:Group,item:Item){if(!writable(item,group.id)||drafts.value[item.id]===states.value[group.id]?.values[item.field])return;const value=drafts.value[item.id];busy.value=item.id;notice.value='';error.value='';try{const state=await api<State>(base+'/settings/'+item.id,'PUT',{expectedValue:states.value[group.id]!.values[item.field],value});if(state.readback!==true||state.values[item.field]!==value)throw new Error('変更後の値を確認できません');receive(group.id,state);notice.value=item.label+'を変更し、本体から再取得しました。'}catch(e){delete states.value[group.id];errors.value[group.id]=message(e);error.value='変更結果を確認できません。状態を更新して確認してください。'}finally{busy.value=''}}
async function prepareFormat(){if(!canFormat.value)return;checking.value=true;error.value='';notice.value='';try{preview.value=await api<Preview>(base+'/sd-format/prepare','POST',{});confirmed.value=false;phrase.value=''}catch(e){error.value=message(e);await loadGroup('storage')}finally{checking.value=false}}
function closeFormat(){if(!busy.value)preview.value=undefined}
async function executeFormat(){if(!canExecute.value||!preview.value)return;busy.value='sd-format';error.value='';notice.value='';const token=preview.value.token;preview.value=undefined;try{const result=await api<{accepted:boolean;completionConfirmed:boolean;values:Values;message:string}>(base+'/sd-format/execute','POST',{token,confirmation:phrase.value,acknowledgeDataLoss:confirmed.value});if(result.accepted!==true)throw new Error('要求の受付を確認できません');notice.value=result.message;pending.value=true;states.value.storage={values:result.values,observedAt:new Date().toISOString()};schedulePoll(0)}catch(e){delete states.value.storage;error.value=message(e)+' 操作を自動で再送しません。'}finally{busy.value='';phrase.value='';confirmed.value=false}}
function schedulePoll(count:number){if(!alive||count>=24)return;poll=setTimeout(async()=>{if(!alive)return;await loadGroup('storage');if(storage.value?.formatStatus===2){pending.value=false;notice.value='本体はフォーマット処理中ではありません。完了結果は本体の音声案内をご確認ください。'}else schedulePoll(count+1)},5000)}
function setOperationCard(element:unknown){if(element instanceof HTMLElement&&!operationCards.includes(element))operationCards.push(element)}
function layoutOperationCards(){cancelAnimationFrame(layoutFrame);layoutFrame=requestAnimationFrame(()=>{for(const card of operationCards)card.style.setProperty('--operation-span',String(Math.max(1,Math.ceil(card.getBoundingClientRect().height+18))))})}
function observeOperationCards(){for(const card of operationCards)operationResizeObserver?.observe(card);layoutOperationCards()}
onBeforeUpdate(()=>operationCards=[])
onMounted(()=>{clock=setInterval(()=>now.value=Date.now(),1000);if(props.mode==='all'&&showSettings.value){operationResizeObserver=new ResizeObserver(layoutOperationCards);void nextTick(observeOperationCards)}void load()})
watch([groups,states],()=>void nextTick(observeOperationCards),{deep:true})
onUnmounted(()=>{alive=false;if(poll)clearTimeout(poll);if(clock)clearInterval(clock);cancelAnimationFrame(layoutFrame);operationResizeObserver?.disconnect()})
</script>
<template>
 <div class="native-operations" :class="'native-operations-'+mode" :aria-label="title">
  <div class="native-operations-head"><div><h2>{{title}}</h2><p>{{description}}</p></div><button v-if="content!=='features'||mode!=='all'" class="secondary" :disabled="loading||!!busy||checking||actionBusy||audioBusy" @click="load"><RefreshCw :size="16"/>{{loading?'取得中…':refreshLabel}}</button></div>
  <p v-if="error" class="banner error" role="alert">{{error}}</p><p v-if="notice" class="banner success" role="status">{{notice}}</p>
  <h3 v-if="mode==='all'&&showSettings&&content==='all'" class="native-block-title">基本設定</h3>
  <div v-if="showSettings" :class="{'native-operation-grid':mode==='all','native-operation-sections':scoped}">
  <section v-if="mode==='all'" :ref="setOperationCard" class="panel native-operation-group" aria-label="SDカードの操作"><div class="panel-head"><h2><HardDrive :size="18"/>SDカード</h2><span class="muted">{{statusLabel}}</span></div><div class="native-operation-storage"><div><strong>{{storage?.inserted===1?'SDカード挿入済み':storage?.inserted===0?'SDカード未挿入':'SDカードの状態を確認中'}}</strong><p>フォーマットすると、SDカード内の録画・写真・走行位置情報など、すべてのデータが消えます。</p><p v-if="errors.storage" class="native-operation-error">{{errors.storage}}</p></div><button class="danger" :disabled="!canFormat||checking" @click="prepareFormat">{{checking?'対象を確認中…':'SDカードをフォーマット'}}</button></div></section>
  <section v-for="group in groups" :ref="setOperationCard" :key="group.id" class="panel native-operation-group" :aria-label="group.label+'の本体操作'"><div class="panel-head"><h2>{{group.label}}</h2><span class="muted">{{states[group.id]?'本体から取得':'未取得'}}</span></div><p v-if="errors[group.id]" class="native-operation-error">{{errors[group.id]}}</p><div v-for="item in group.items" :key="item.id" class="native-operation-row"><label :for="'operation-'+item.id">{{item.label}}</label><div v-if="item.options&&item.writable"><select :id="'operation-'+item.id" v-model="drafts[item.id]" :disabled="!writable(item,group.id)"><option v-if="!item.options.some(o=>o.value===states[group.id]?.values[item.field])" :value="states[group.id]?.values[item.field]">{{show(item,group.id)}}</option><option v-for="option in item.options" :key="option.value" :value="option.value">{{option.label}}</option></select><button class="primary" :disabled="!writable(item,group.id)||drafts[item.id]===states[group.id]?.values[item.field]" @click="apply(group,item)">{{busy===item.id?'確認中…':'変更を適用'}}</button></div><strong v-else>{{show(item,group.id)}}</strong></div></section>
  <RecorderAdvancedSettings v-if="ready&&mode==='recorder'"/>
  <NativeAudioSettings v-if="ready&&mode==='audio'" :show-master-volume="false" @busy="audioBusy=$event"/>
  </div>
  <template v-if="showActions">
   <h3 v-if="content==='all'" class="native-block-title">高度な本体操作</h3>
   <NativeActionCards :actions="actions" :disabled="!available||loading||!!busy" @busy="actionBusy=$event"/>
  </template>
  <template v-if="showFeatures">
   <h3 v-if="content==='all'" class="native-block-title">関連機能</h3>
   <div class="native-feature-grid"><section v-for="group in nativeAppFeatures" :key="group.group" class="panel native-operation-features" :aria-label="group.group"><div class="panel-head"><h2><Smartphone :size="18"/>{{group.group}}</h2></div><div class="native-operation-feature-group"><a v-for="item in group.items" :key="item.name" :href="item.href" :target="item.app?'_blank':undefined" :rel="item.app?'noopener noreferrer':undefined"><div><strong>{{item.name}}</strong><p>{{item.description}}</p></div><span>{{item.app?'My NP1の操作方法':'画面を開く'}}<ArrowUpRight :size="14"/></span></a></div></section></div>
  </template>
  <div v-if="preview" class="modal-backdrop" @click.self="closeFormat"><section class="modal native-format-modal" role="dialog" aria-modal="true" aria-labelledby="sd-format-title"><h2 id="sd-format-title"><AlertTriangle :size="22"/>SDカードをフォーマット</h2><p>対象：NP1に挿入されているSDカード（{{bytes(preview.capacityBytes)}}）</p><p class="native-format-warning">SDカードのすべてのデータを消去します。録画・写真・走行位置情報は元に戻せません。必要なデータを先に保存してください。</p><p>処理中は電源を切ったり、SDカードを抜いたりしないでください。本体内に独立して保存した走行履歴やNP1modの設定は、この操作の消去対象ではありません。</p><label class="native-format-ack"><input type="checkbox" v-model="confirmed">必要なデータを保存し、SDカードの全データ消去を確認しました</label><label class="native-format-phrase">確認のため「SDカードを消去」と入力してください<input v-model="phrase" autocomplete="off" placeholder="SDカードを消去"></label><p class="muted">確認の有効期限は1分です。カードを交換した場合は確認し直してください。</p><div class="modal-actions"><button class="secondary" @click="closeFormat">キャンセル</button><button class="danger" :disabled="!canExecute" @click="executeFormat">SDカードの全データを消去してフォーマット</button></div></section></div>
 </div>
</template>
<style scoped>
.native-operation-grid,.native-feature-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(min(100%,380px),1fr));column-gap:18px;align-items:start;min-width:0}.native-operation-grid{grid-auto-flow:row dense;grid-auto-rows:1px;row-gap:0}.native-feature-grid{row-gap:18px}.native-operation-sections{display:grid;gap:18px;min-width:0}.native-operation-grid>.panel,.native-operation-sections>.panel,.native-feature-grid>.panel{min-width:0;container-type:inline-size}.native-operation-grid>.panel{grid-row-end:span var(--operation-span,1)}.native-operation-grid .panel-head{flex-wrap:wrap;gap:10px}.native-operation-grid .native-operation-storage{align-items:stretch;flex-direction:column}.native-operation-grid .native-operation-row{flex-wrap:wrap}.native-operation-grid .native-operation-row>div{flex:1;min-width:0;justify-content:flex-end}.native-operation-grid .native-operation-row select{min-width:0;flex:1}.native-operation-grid .native-operation-row button{flex:none}.native-feature-grid .native-operation-feature-group a{flex-direction:column;align-items:flex-start;gap:8px}.native-feature-grid .native-operation-feature-group{padding-top:4px}
@container(max-width:520px){.native-operation-row{align-items:stretch;flex-direction:column;gap:10px}.native-operation-row>div{justify-content:space-between}.native-operation-row select{max-width:none}:deep(.native-volume){align-items:flex-start;flex-direction:column;gap:10px}:deep(.native-audio-grid){grid-template-columns:1fr;gap:24px}}
.native-format-modal{max-height:calc(100dvh - 40px);overflow-y:auto;overscroll-behavior:contain}
.danger{display:inline-flex;align-items:center;justify-content:center;gap:8px;min-height:44px;padding:11px 16px;border-radius:var(--radius-md);background:#b42318;color:#fff;font-size:12px;font-weight:650;line-height:1.7}.danger:hover:not(:disabled){background:#912018}.danger:disabled{opacity:.45;cursor:not-allowed}
.native-operations{display:grid;gap:18px;margin-bottom:24px;min-width:0}.native-operations-audio{margin-bottom:0;gap:20px}.native-operations-audio .native-operation-sections{gap:20px}.native-operations-head{display:flex;align-items:center;justify-content:space-between;gap:16px}.native-operations-head h2{font-size:18px}.native-operations-head p,.native-operation-storage p{font-size:12px;line-height:1.8;color:var(--muted);margin-top:6px}.native-operations-head button{white-space:nowrap;min-height:44px}.native-block-title{font-size:14px;margin-top:6px;color:var(--muted);letter-spacing:.02em}.native-operation-group .panel-head h2,.native-operation-features h2{display:flex;align-items:center;gap:9px}.native-operation-group .panel-head .muted{font-size:11px}.native-operation-storage{display:flex;align-items:center;justify-content:space-between;gap:18px;padding:20px 24px}.native-operation-storage strong{font-size:14px}.native-operation-storage button{flex:none}.native-operation-row{display:flex;align-items:center;justify-content:space-between;gap:16px;padding:16px 24px;border-top:1px solid var(--line);font-size:13px}.native-operation-row>label{font-weight:550}.native-operation-row>div{display:flex;gap:10px;align-items:center;flex:none}.native-operation-row select{max-width:200px;min-height:44px}.native-operation-row button{min-height:44px;font-size:12px}.native-operation-row>strong{font-size:13px;font-weight:500}.native-operation-error{padding:10px 24px;color:var(--danger);font-size:12px;line-height:1.7}.native-operation-feature-note{font-size:12px;color:var(--muted);padding:18px 24px;line-height:1.8}.native-operation-feature-group{padding:0 24px 18px}.native-operation-feature-group h3{font-size:13px;padding:14px 0;color:var(--muted)}.native-operation-feature-group a{display:flex;align-items:center;justify-content:space-between;gap:16px;padding:14px 0;border-top:1px solid var(--line);color:var(--text);text-decoration:none}.native-operation-feature-group strong{font-size:13px}.native-operation-feature-group p{font-size:12px;color:var(--muted);line-height:1.8;margin-top:5px}.native-operation-feature-group a>span{display:flex;gap:5px;align-items:center;white-space:nowrap;font-size:11px;color:var(--accent)}.native-format-modal h2{display:flex;align-items:center;gap:9px;color:var(--danger);font-size:18px}.native-format-modal>p{font-size:13px;line-height:1.9;margin-top:14px}.native-format-warning{font-weight:600}.native-format-ack{display:flex;gap:10px;align-items:flex-start;font-size:13px;line-height:1.8;margin-top:18px}.native-format-ack input{width:18px;height:18px;flex:none;margin-top:3px}.native-format-phrase{display:grid;gap:9px;margin-top:18px;font-size:12px}.native-format-modal .modal-actions{display:flex;justify-content:flex-end;flex-wrap:wrap;gap:10px;margin-top:18px}.native-format-modal .danger{white-space:normal}.native-operations .banner{margin:0}@media(max-width:600px){.native-operations-audio,.native-operations-audio .native-operation-sections{gap:16px}.native-operations-head{align-items:stretch;flex-direction:column}.native-operation-storage{align-items:stretch;flex-direction:column;padding:18px}.native-operation-row{align-items:stretch;flex-direction:column;padding:16px 18px;gap:10px}.native-operation-row>div{justify-content:space-between}.native-operation-row select{max-width:70%;min-width:0}.native-operation-feature-note{padding:16px 18px}.native-operation-feature-group{padding-inline:18px}.native-operation-feature-group a{align-items:flex-start;flex-direction:column;gap:7px}}
</style>
