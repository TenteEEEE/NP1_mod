<script setup lang="ts">
import { computed,onMounted,ref } from 'vue'
import { Check,Grid2X2,RefreshCw,Settings2,SlidersHorizontal,Wrench } from '@lucide/vue'
import { api,type Observation } from '../services/api'
import { nativePages } from '../services/native'
import AlexaSettings from './AlexaSettings.vue'
import NativeOperations from './NativeOperations.vue'
type Item={id:string;label:string;description?:string;kind:'boolean'|'select'|'range'|'info';value:unknown;valueLabel?:string;options?:{value:unknown;label:string}[];min?:number;max?:number;step?:number;writable:boolean;reason?:string;group:string;status?:string}
type NativeData={items:Item[];revision?:number;scope?:string}
const props=defineProps<{section?:string}>()
type NativeView='settings'|'actions'|'features'
type SettingView='device'|'support'|'navigation'|'privacy'
const activeView=ref<NativeView>('settings')
const activeSettings=ref<SettingView>('device')
const views:{id:NativeView;label:string;description:string}[]=[
 {id:'settings',label:'設定',description:'日常的に確認・変更する項目'},
 {id:'actions',label:'操作・診断',description:'取付調整、接続、保守'},
 {id:'features',label:'関連機能',description:'Control CenterとMy NP1'},
]
const settingViews:{id:SettingView;label:string;description:string;pages:string[]}[]=[
 {id:'device',label:'本体',description:'Alexa・SDカード・車両',pages:[]},
 {id:'support',label:'運転支援',description:'通知・検知',pages:['support']},
 {id:'navigation',label:'ナビ・案内',description:'経路・音声',pages:['navigation','guidance']},
 {id:'privacy',label:'プライバシー・取付',description:'カメラ・取付情報',pages:['privacy','installation']},
]
const selectedSettings=computed(()=>settingViews.find(view=>view.id===activeSettings.value)??settingViews[0]!)
const state=ref<Observation<NativeData>>(),drafts=ref<Record<string,unknown>>({}),loading=ref(false),saving=ref(''),error=ref(''),notice=ref('')
const items=computed(()=>{
 const all=state.value?.data?.items||[],pages=props.section?nativePages.filter(p=>p.id===props.section):nativePages.filter(page=>selectedSettings.value.pages.includes(page.id))
 return pages.flatMap(page=>page.items.map((id,index)=>{const item=all.find(item=>item.id===id);if(item)return {...item,group:page.label};if(page.id==='installation')return {id,label:['車種','取付高さ','取付位置','取付方向'][index]!,group:page.label,kind:'info',value:null,writable:false,status:'unknown',reason:'この項目は現在の本体から取得できません。'} as Item;return undefined}).filter((item):item is Item=>!!item))
})
const groups=computed(()=>[...new Set(items.value.map(i=>i.group))])
const count=computed(()=>items.value.filter(i=>i.value!==null&&i.value!==undefined).length)
function display(item:Item){if(item.value===null||item.value===undefined)return '未取得';if(item.valueLabel)return item.valueLabel;if(typeof item.value==='boolean')return item.value?'ON':'OFF';const option=item.options?.find(o=>o.value===item.value);return option?.label||String(item.value)}
function editable(item:Item){return state.value?.status==='available'&&item.status==='available'&&item.writable&&item.value!==null&&item.value!==undefined&&!loading.value&&!saving.value}
function changed(item:Item){return JSON.stringify(drafts.value[item.id])!==JSON.stringify(item.value)}
function receive(value:Observation<NativeData>){state.value=value;drafts.value=Object.fromEntries((value.data?.items||[]).map(i=>[i.id,i.value]))}
async function load(){if(loading.value||saving.value)return;loading.value=true;error.value='';notice.value='';try{receive(await api<Observation<NativeData>>('/device/native-settings'))}catch(e){state.value=undefined;error.value='純正設定を取得できませんでした。'+String(e)}finally{loading.value=false}}
async function apply(item:Item){if(!editable(item)||!changed(item))return;const wanted=drafts.value[item.id];saving.value=item.id;error.value='';notice.value='';try{receive(await api<Observation<NativeData>>('/device/native-settings/'+encodeURIComponent(item.id),'PUT',{expectedValue:item.value,value:wanted}));const current=state.value?.data?.items.find(i=>i.id===item.id);if(state.value?.status!=='available'||current?.status!=='available'||JSON.stringify(current.value)!==JSON.stringify(wanted))throw new Error('変更後の値を確認できません。状態を更新してください。');notice.value=item.label+'を変更し、端末から再取得しました。'}catch(e){state.value=undefined;error.value='変更を確認できませんでした。状態を更新してください。'+String(e)}finally{saving.value=''}}
onMounted(load)
</script>
<template>
 <template v-if="!section">
  <header class="native-page-head">
   <div><span>NP1 SETTINGS</span><h1>純正設定</h1><p>NP1本体の設定、診断、関連機能を目的別にまとめています。</p></div>
  </header>
  <nav class="native-view-tabs" aria-label="純正設定のカテゴリ">
   <button v-for="view in views" :key="view.id" type="button" :class="{active:activeView===view.id}" :aria-current="activeView===view.id?'page':undefined" @click="activeView=view.id">
    <SlidersHorizontal v-if="view.id==='settings'" :size="18"/><Wrench v-else-if="view.id==='actions'" :size="18"/><Grid2X2 v-else :size="18"/>
    <span><strong>{{view.label}}</strong><small>{{view.description}}</small></span>
   </button>
  </nav>
  <template v-if="activeView==='settings'">
  <nav class="native-settings-tabs" aria-label="設定項目">
   <button v-for="view in settingViews" :key="view.id" type="button" :class="{active:activeSettings===view.id}" :aria-current="activeSettings===view.id?'page':undefined" @click="activeSettings=view.id"><strong>{{view.label}}</strong><small>{{view.description}}</small></button>
  </nav>
  <div v-if="activeSettings==='device'" class="native-view native-settings-view">
   <div class="native-alexa"><AlexaSettings/></div>
   <NativeOperations content="settings"/>
  </div>
  </template>
  <NativeOperations v-else-if="activeView==='actions'" content="actions" class="native-view"/>
  <NativeOperations v-else content="features" class="native-view"/>
 </template>
 <template v-if="section||(activeView==='settings'&&activeSettings!=='device')">
 <div v-if="!section" class="native-preferences-head"><div><span>STOCK SERVICE</span><h2>{{selectedSettings.label}}</h2><p>{{selectedSettings.description}}に関する純正サービスの設定値を確認・変更します。</p></div></div>
 <div class="native-toolbar"><p>NP1の現在の設定を確認します。</p><button class="secondary" @click="load" :disabled="loading||!!saving"><RefreshCw :size="16"/>{{loading?'読込み中…':'状態を更新'}}</button></div>
 <p v-if="error" class="banner error" role="alert">{{error}}</p><p v-if="notice" class="banner success" role="status"><Check :size="17"/>{{notice}}</p>
 <div v-if="loading&&!state" class="panel native-empty" role="status">純正設定を読み込んでいます…</div>
 <template v-else-if="items.length">
  <p v-if="state?.reason" class="hint">{{state.reason}}</p><div class="native-summary"><span><strong>{{count}}</strong> / {{items.length}} 項目を取得</span></div>
  <section v-for="group in groups" :key="group" class="panel native-group"><div class="panel-head"><h2><Settings2 :size="18"/>{{group}}</h2><span class="muted">純正NP1の設定</span></div>
   <div v-for="item in items.filter(i=>i.group===group)" :key="item.id" class="native-row"><div class="native-description"><h3>{{item.label}}</h3><p v-if="item.description">{{item.description}}</p></div>
    <div class="native-value"><template v-if="item.writable&&item.kind!=='info'">
     <label v-if="item.kind==='boolean'" class="native-toggle"><input type="checkbox" v-model="drafts[item.id]" :disabled="!editable(item)" :aria-label="item.label">{{drafts[item.id]?'ON':'OFF'}}</label>
     <select v-else-if="item.kind==='select'" v-model="drafts[item.id]" :disabled="!editable(item)" :aria-label="item.label"><option v-for="option in item.options" :key="String(option.value)" :value="option.value">{{option.label}}</option></select>
     <label v-else class="native-range"><input type="range" v-model.number="drafts[item.id]" :min="item.min" :max="item.max" :step="item.step||1" :disabled="!editable(item)" :aria-label="item.label"><output>{{drafts[item.id]}}</output></label>
     <button class="primary" @click="apply(item)" :disabled="!editable(item)||!changed(item)">{{saving===item.id?'確認中…':'変更を適用'}}</button>
    </template><template v-else><strong>{{display(item)}}</strong><span v-if="item.value!==null&&item.value!==undefined" class="native-badge">読取り</span></template></div>
    <p v-if="item.reason" class="native-reason">{{item.reason}}</p>
   </div>
  </section>
 </template>
 <section v-else-if="!loading" class="panel native-empty"><h2>純正設定はまだ取得できません</h2><p>{{state?.reason||'対応する設定取得経路を確認しています。'}}</p><button class="secondary" @click="load">もう一度取得</button></section>
 </template>
</template>
<style scoped>
.native-page-head{display:flex;align-items:flex-end;justify-content:space-between;gap:24px;margin-bottom:20px}.native-page-head span,.native-preferences-head span{display:block;color:var(--accent);font-size:10px;font-weight:750;letter-spacing:.14em;margin-bottom:7px}.native-page-head h1{font-size:25px;line-height:1.2;letter-spacing:-.02em}.native-page-head p,.native-preferences-head p{font-size:12px;line-height:1.8;color:var(--muted);margin-top:7px}.native-view-tabs{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:10px;padding:6px;margin-bottom:14px;border:1px solid var(--line);border-radius:var(--radius-lg);background:color-mix(in srgb,var(--panel) 82%,var(--bg))}.native-view-tabs button{display:flex;align-items:center;gap:11px;min-width:0;min-height:64px;padding:11px 15px;border:0;border-radius:calc(var(--radius-lg) - 6px);background:transparent;color:var(--muted);text-align:left;transition:background-color .15s,color .15s,box-shadow .15s}.native-view-tabs button:hover{background:var(--panel);color:var(--text)}.native-view-tabs button.active{background:var(--panel);color:var(--text);box-shadow:0 1px 3px rgb(15 23 42/.12)}.native-view-tabs button.active>svg{color:var(--accent)}.native-view-tabs button>svg{flex:none}.native-view-tabs button span{display:grid;gap:3px;min-width:0}.native-view-tabs strong{font-size:13px}.native-view-tabs small{overflow:hidden;color:var(--muted);font-size:10px;font-weight:450;text-overflow:ellipsis;white-space:nowrap}.native-settings-tabs{display:flex;gap:6px;margin-bottom:24px;padding-bottom:10px;border-bottom:1px solid var(--line);overflow-x:auto}.native-settings-tabs button{display:grid;gap:2px;min-width:150px;padding:9px 13px;border:0;border-radius:9px;background:transparent;color:var(--muted);text-align:left;white-space:nowrap}.native-settings-tabs button:hover{background:var(--panel);color:var(--text)}.native-settings-tabs button.active{background:var(--accent-soft);color:var(--accent)}.native-settings-tabs strong{font-size:12px}.native-settings-tabs small{font-size:10px;color:inherit;opacity:.8}.native-view{animation:native-view-in .16s ease-out}.native-preferences-head{display:flex;align-items:flex-end;justify-content:space-between;margin:4px 0 14px}.native-preferences-head h2{font-size:18px}.native-settings-view>.native-operations{margin-bottom:0}@keyframes native-view-in{from{opacity:.45;transform:translateY(3px)}to{opacity:1;transform:none}}
.native-toolbar{display:flex;justify-content:space-between;align-items:center;gap:20px;margin-bottom:20px}.native-toolbar p{font-size:13px;color:var(--muted)}.native-toolbar button{white-space:nowrap;min-height:44px}.native-summary{display:flex;justify-content:space-between;gap:16px;align-items:center;margin:20px 0;font-size:12px;color:var(--muted)}.native-summary strong{font-size:20px;color:var(--text);font-weight:650}.native-group{margin-bottom:20px}.native-group .panel-head h2{display:flex;align-items:center;gap:10px}.native-group .panel-head h2 svg{color:var(--accent)}.native-group .panel-head .muted{font-size:11px}.native-row{display:grid;grid-template-columns:minmax(0,1fr) auto;gap:8px 22px;padding:20px 24px;border-top:1px solid var(--line)}.native-description h3{font-size:14px;font-weight:650}.native-description p{font-size:12px;color:var(--muted);margin-top:6px}.native-value{display:flex;align-items:center;gap:12px;flex-wrap:wrap;justify-content:flex-end}.native-value>strong{font-size:16px;font-weight:550}.native-badge{font-size:11px;background:var(--bg);border:1px solid var(--line);padding:4px 7px;border-radius:4px;color:var(--muted)}.native-reason{grid-column:1/-1;font-size:11px;color:var(--muted);line-height:1.7}.native-value .primary{font-size:12px;min-height:44px}.native-value select{max-width:220px;min-height:44px}.native-toggle{display:flex;align-items:center;gap:10px;min-height:44px}.native-toggle{font-size:13px;font-weight:600;min-width:86px;cursor:pointer}.native-toggle input{appearance:none;-webkit-appearance:none;position:relative;width:44px;height:26px;margin:0;border-radius:999px;background:var(--line-strong);cursor:pointer;flex-shrink:0;transition:background-color .15s}.native-toggle input::before{content:"";position:absolute;top:3px;left:3px;width:20px;height:20px;border-radius:50%;background:#fff;box-shadow:0 1px 3px rgb(16 24 40/.3);transition:transform .15s}.native-toggle input:checked{background:var(--accent)}.native-toggle input:checked::before{transform:translateX(18px)}.native-toggle input:disabled{opacity:.5;cursor:not-allowed}.native-value .primary:disabled{background:transparent;color:var(--muted);border:1px solid var(--line);box-shadow:none;opacity:.8}.native-range{display:flex;align-items:center;gap:10px}.native-range input{min-height:44px;padding:0;max-width:160px;accent-color:var(--accent)}.native-empty{padding:35px;text-align:center}.native-empty h2{font-size:17px}.native-empty p{margin:14px auto;color:var(--muted);font-size:13px;max-width:560px}@media(max-width:600px){.native-toolbar{align-items:flex-start;flex-direction:column;gap:12px}.native-toolbar button{width:100%}.native-summary{align-items:flex-start;flex-direction:column;gap:5px}.native-row{grid-template-columns:1fr;padding:18px 20px;gap:12px}.native-value{justify-content:flex-start}.native-reason{font-size:11px}.native-value .primary{margin-left:auto}.native-group .panel-head .muted{display:none}.native-empty{padding:25px 20px}}
</style>
<style scoped>
.native-alexa{margin-bottom:20px}@media(max-width:700px){.native-page-head h1{font-size:22px}.native-view-tabs{gap:5px;padding:5px}.native-view-tabs button{justify-content:center;min-height:54px;padding:9px 6px;text-align:center}.native-view-tabs button>svg{display:none}.native-view-tabs small{display:none}.native-settings-tabs{margin-inline:-14px;padding-inline:14px}.native-settings-tabs button{min-width:auto;flex:none}.native-settings-tabs small{display:none}.native-preferences-head{margin-top:4px}}
</style>
