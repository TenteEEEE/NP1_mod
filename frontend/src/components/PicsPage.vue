<script setup lang="ts">
import {nextTick,onMounted,onUnmounted,ref} from 'vue'
import {Radio} from '@lucide/vue'
import {api} from '../services/api'
import {isPicsStatus,picsCourseText,picsSignalText,picsStatusText,type PicsSettings,type PicsSpeech,type PicsStatus} from '../services/pics'
import PicsHistory from './PicsHistory.vue'
import '../plate-recognition.css'

const tabs=[{id:'records',label:'受信記録'},{id:'settings',label:'受信・発話設定'}] as const
const tab=ref<(typeof tabs)[number]['id']>('records')
const tablist=ref<HTMLElement>()
const draft=ref<PicsSettings>(),status=ref<PicsStatus>(),speech=ref<PicsSpeech>()
const busy=ref(false),playing=ref(false),error=ref(''),notice=ref('')
let timer:ReturnType<typeof setInterval>|undefined
const message=(e:unknown)=>e instanceof Error?e.message:String(e)

async function moveTab(event:KeyboardEvent){
 if(!['ArrowLeft','ArrowRight','Home','End'].includes(event.key))return
 event.preventDefault()
 tab.value=event.key==='Home'?'records':event.key==='End'?'settings':tab.value==='records'?'settings':'records'
 await nextTick()
 tablist.value?.querySelector<HTMLElement>('[aria-selected=true]')?.focus()
}

async function refresh(){
 try{
  const next=await api<PicsStatus>('/pics')
  if(!isPicsStatus(next))throw new Error('高度化PICSの設定を取得できません')
  status.value=next
  if(!draft.value){draft.value={...next.settings};error.value=''}
  if(next.settings.enabled&&next.settings.speech)speech.value=await api<PicsSpeech>('/pics/speech')
 }catch(e){if(!status.value)error.value=message(e)}
}

async function save(){
 if(!draft.value)return
 busy.value=true;notice.value='';error.value=''
 try{draft.value=await api<PicsSettings>('/pics','PUT',draft.value);await refresh();notice.value='保存しました'}
 catch(e){error.value=message(e)}
 finally{busy.value=false}
}

async function preview(){
 playing.value=true;error.value='';notice.value=''
 try{await api('/pics/speech/preview','POST');notice.value='本体で再生しました'}
 catch(e){error.value=message(e)}
 finally{playing.value=false}
}

onMounted(()=>{void refresh();timer=setInterval(()=>void refresh(),3000)})
onUnmounted(()=>clearInterval(timer))
</script>

<template>
 <div class="plate-page">
  <section class="panel plate-summary" aria-labelledby="pics-title">
   <div class="plate-summary-main"><Radio :size="22" aria-hidden="true"/><div><h2 id="pics-title">高度化PICS（実験機能）</h2></div></div>
   <dl v-if="status" class="plate-chips">
    <div><dt>受信</dt><dd :class="status.state==='unavailable'?'warn':status.settings.enabled?'on':'off'">{{status.state==='unavailable'?'BLE受信不可':picsStatusText(status)}}</dd></div>
    <div><dt>発話</dt><dd :class="status.settings.speech?'on':'off'">{{status.settings.speech?(speech?.prepared?'準備済み':'有効'):'オフ'}}</dd></div>
   </dl>
  </section>
  <p v-if="status?.state==='unavailable'&&status.reason" class="banner neutral" role="note">{{status.reason}}</p>
  <nav ref="tablist" class="plate-tabs" role="tablist" aria-label="高度化PICS" @keydown="moveTab">
   <button v-for="item in tabs" :id="`pics-tab-${item.id}`" :key="item.id" type="button" role="tab" :aria-controls="`pics-panel-${item.id}`" :aria-selected="tab===item.id" :tabindex="tab===item.id?0:-1" :class="{active:tab===item.id}" @click="tab=item.id">{{item.label}}</button>
  </nav>
  <div v-show="tab==='records'" id="pics-panel-records" role="tabpanel" aria-labelledby="pics-tab-records">
   <PicsHistory :live="status?.signals??[]"/>
  </div>
  <section v-show="tab==='settings'" id="pics-panel-settings" class="panel" role="tabpanel" aria-labelledby="pics-tab-settings">
  <div class="settings-body pics-settings">
   <form v-if="draft" @submit.prevent="save">
    <label class="pics-check"><input v-model="draft.enabled" type="checkbox" :disabled="busy">信号をダッシュボードに表示する</label>
    <label class="pics-check"><input v-model="draft.speech" type="checkbox" :disabled="busy||!draft.enabled">VOICEVOXで本体から発話する</label>
    <label class="pics-check"><input v-model="draft.directional" type="checkbox" :disabled="busy||!draft.enabled">進行方向で絞り込む</label>
    <p v-if="draft.directional" class="hint">進行方向 {{picsCourseText(status?.course)}}。受信記録で交差点ごとに灯器の横断方向を登録してください。</p>
    <label class="pics-check"><input v-model="draft.led" type="checkbox" :disabled="busy||!draft.enabled">信号の状態を本体LEDに表示する</label>
    <p v-if="draft.led" class="hint">赤は左、青点滅は中央、青は右に表示します。</p>
    <template v-if="draft.enabled&&draft.speech">
     <div class="pics-voice-controls">
      <label>声<select v-model.number="draft.style" :disabled="busy">
       <option v-if="!speech?.styles.some(style=>style.id===draft?.style)" :value="draft.style">{{draft.style===1?'ずんだもん（あまあま）':`スタイル ${draft.style}`}}</option>
       <option v-for="style in speech?.styles" :key="style.id" :value="style.id">{{style.speaker}}（{{style.name}}）</option>
      </select></label>
      <label>発話音量 {{draft.volume}}%<input v-model.number="draft.volume" type="range" min="0" max="100" :disabled="busy"></label>
     </div>
     <p class="hint">APIキーは「システム › LED / 音声」で設定できます。音声は本体に保存されます。</p>
     <p v-if="status?.settings.speech" class="hint">{{speech?.reason||(speech?.prepared?'読み上げ音声を準備済み':'読み上げ音声を準備中')}}</p>
     <p v-if="speech?.credit" class="hint">{{speech.credit}}</p>
    </template>
    <div class="pics-actions">
     <button type="submit" class="primary" :disabled="busy||playing">{{busy?'保存中…':'保存'}}</button>
     <button v-if="draft.enabled&&draft.speech" type="button" class="secondary" :disabled="busy||playing||!speech?.ready||!status?.settings.speech||status.settings.volume===0||draft.style!==status.settings.style||draft.volume!==status.settings.volume" @click="preview">{{playing?'再生中…':'本体で試聴'}}</button>
    </div>
   </form>
   <ul v-if="status?.signals.length" class="pics-signals">
    <li v-for="signal in status.signals.slice(0,12)" :key="`${signal.id}-${signal.slot}`"><span>{{signal.name||signal.id}} 信号{{signal.slot}}<small v-if="signal.directionMatch"> · 進行方向</small></span><strong>{{picsSignalText(signal)}}</strong></li>
   </ul>
   <p v-if="notice" class="hint" role="status">{{notice}}</p>
   <p v-if="error" class="error" role="alert">{{error}}</p>
  </div>
  </section>
 </div>
</template>

<style scoped>
.pics-settings,.pics-settings form{display:grid;gap:12px}.pics-settings p{margin:0}.pics-check{display:flex;align-items:center;gap:8px}.pics-actions,.pics-voice-controls{display:flex;flex-wrap:wrap;align-items:center;gap:10px}.pics-voice-controls label{display:grid;gap:4px}.pics-signals{display:grid;gap:6px;list-style:none;margin:0;padding:0}.pics-signals li{display:flex;justify-content:space-between;gap:10px;align-items:baseline}
</style>
