<script setup lang="ts">
import {onMounted,ref} from 'vue'
import {Gauge,RefreshCw,VideoOff} from '@lucide/vue'
import {api} from '../services/api'

type Settings={frontBitrateKbps:number;cabinBitrateKbps:number;frontRateControl:'cbr'|'vbr';cabinRateControl:'cbr'|'vbr';frontGopMilliseconds:number;cabinGopMilliseconds:number;cabinRecording:boolean}
type State={available:boolean;configured:boolean;hookLoaded:boolean;restartRequired:boolean;generation:number;settings:Settings;applied?:boolean;message?:string}
const state=ref<State>(),draft=ref<Settings>(),loading=ref(false),saving=ref(false),acknowledged=ref(false),error=ref(''),notice=ref('')
const clone=(value:Settings):Settings=>({...value})
const message=(value:unknown)=>value instanceof Error?value.message:String(value)
async function load(){if(loading.value||saving.value)return;loading.value=true;error.value='';try{state.value=await api<State>('/device/recorder-advanced');draft.value=clone(state.value.settings)}catch(e){error.value=message(e);state.value=undefined;draft.value=undefined}finally{loading.value=false}}
async function apply(){if(!state.value?.available||!draft.value||!acknowledged.value||saving.value)return;saving.value=true;error.value='';notice.value='';try{const next=await api<State>('/device/recorder-advanced','PUT',{expectedGeneration:state.value.generation,settings:draft.value,applyNow:true,confirmation:'録画サービスを再起動',acknowledgeRecordingGap:true});state.value=next;draft.value=clone(next.settings);acknowledged.value=false;notice.value=next.message||'録画設定を保存しました。'}catch(e){error.value=message(e);await load()}finally{saving.value=false}}
onMounted(load)
</script>
<template>
 <section class="panel recorder-advanced" aria-label="H.264詳細録画設定">
  <div class="panel-head"><h2><Gauge :size="18"/>H.264詳細録画設定</h2><button class="secondary compact" :disabled="loading||saving" @click="load"><RefreshCw :size="14" :class="{spinning:loading}"/>{{loading?'取得中…':'再取得'}}</button></div>
  <p v-if="error" class="native-operation-error" role="alert">{{error}}</p><p v-if="notice" class="recorder-notice" role="status">{{notice}}</p>
  <div v-if="draft" class="recorder-advanced-body">
   <p class="recorder-hook-state"><b>{{state?.applied?'設定世代を適用済み':state?.hookLoaded?'フック読込み済み・設定再適用待ち':state?.available?'設定可能・再起動待ち':'このファームウェアでは利用不可'}}</b><span>profile／level指定は未対応です。設定値は次のDriveRec起動時に適用されます。</span></p>
   <div class="recorder-camera-grid">
    <fieldset><legend>前方カメラ</legend><label>ビットレート（kbps）<input v-model.number="draft.frontBitrateKbps" type="number" min="128" max="50000" step="1" :disabled="!state?.available||saving"></label><label>レート制御<select v-model="draft.frontRateControl" :disabled="!state?.available||saving"><option value="vbr">可変（VBR）</option><option value="cbr">固定（CBR）</option></select></label><label>GOP間隔（ms）<input v-model.number="draft.frontGopMilliseconds" type="number" min="250" max="10000" step="10" :disabled="!state?.available||saving"></label></fieldset>
    <fieldset><legend>車内・後方カメラ</legend><label class="recorder-enabled"><input v-model="draft.cabinRecording" type="checkbox" :disabled="!state?.available||saving"><span><b>録画する</b><small>OFFでは車内側の通常録画開始を抑止します</small></span></label><label>ビットレート（kbps）<input v-model.number="draft.cabinBitrateKbps" type="number" min="128" max="50000" step="1" :disabled="!state?.available||saving||!draft.cabinRecording"></label><label>レート制御<select v-model="draft.cabinRateControl" :disabled="!state?.available||saving||!draft.cabinRecording"><option value="vbr">可変（VBR）</option><option value="cbr">固定（CBR）</option></select></label><label>GOP間隔（ms）<input v-model.number="draft.cabinGopMilliseconds" type="number" min="250" max="10000" step="10" :disabled="!state?.available||saving||!draft.cabinRecording"></label></fieldset>
   </div>
   <label class="recorder-restart-ack"><input v-model="acknowledged" type="checkbox" :disabled="!state?.available||saving"><span><b>録画サービスの再起動を許可する</b><small>適用時に録画が数秒間中断します。走行中の変更は避けてください。</small></span></label>
   <div class="recorder-actions"><span><VideoOff :size="15"/>純正ファイルは変更せず、プロセス終了時にフックは消えます。</span><button class="primary" :disabled="!state?.available||!acknowledged||saving" @click="apply">{{saving?'再起動と読込みを確認中…':'保存して録画サービスを再起動'}}</button></div>
  </div>
 </section>
</template>
<style scoped>
.recorder-advanced{min-width:0}.panel-head h2,.panel-head button,.recorder-actions span{display:flex;align-items:center;gap:8px}.recorder-advanced-body{padding:18px 24px 22px;display:grid;gap:18px}.recorder-hook-state{display:grid;gap:5px;font-size:12px}.recorder-hook-state span,.recorder-actions span{color:var(--muted);line-height:1.7}.recorder-camera-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:16px}.recorder-camera-grid fieldset{border:1px solid var(--line);border-radius:var(--radius-md);padding:14px;display:grid;gap:12px;min-width:0}.recorder-camera-grid legend{padding:0 6px;font-size:13px;font-weight:650}.recorder-camera-grid label{display:grid;gap:6px;font-size:11px;color:var(--muted)}.recorder-camera-grid input[type=number],.recorder-camera-grid select{width:100%;min-height:44px}.recorder-enabled,.recorder-restart-ack{display:flex!important;grid-template-columns:none!important;align-items:flex-start;gap:10px!important;color:var(--text)!important}.recorder-enabled input,.recorder-restart-ack input{width:18px;height:18px;flex:none;margin-top:2px}.recorder-enabled span,.recorder-restart-ack span{display:grid;gap:3px}.recorder-enabled small,.recorder-restart-ack small{color:var(--muted);line-height:1.6}.recorder-restart-ack{padding:13px;border-radius:var(--radius-md);background:color-mix(in srgb,var(--warning) 9%,transparent);font-size:12px}.recorder-actions{display:flex;justify-content:space-between;align-items:center;gap:16px}.recorder-actions span{font-size:11px}.recorder-actions button{min-height:44px}.recorder-notice{margin:12px 24px 0;padding:10px 12px;border-radius:var(--radius-sm);background:var(--success-soft);font-size:12px}@media(max-width:650px){.recorder-advanced-body{padding:16px 18px}.recorder-camera-grid{grid-template-columns:1fr}.recorder-actions{align-items:stretch;flex-direction:column}.recorder-actions button{width:100%}}
</style>
