<script setup lang="ts">
import {computed,onMounted,onUnmounted,ref} from 'vue'
import {api} from '../services/api'
import {isPicsStatus,picsSignalText,picsStatusText,type PicsStatus} from '../services/pics'

const status=ref<PicsStatus>()
const signal=computed(()=>status.value?.settings.directional?status.value.signals.find(s=>s.directionMatch):status.value?.signals[0])
const text=computed(()=>{
 if(signal.value)return picsSignalText(signal.value)
 const value=status.value
 if(!value)return ''
 if(value.state==='unavailable')return 'BLE受信不可'
 if(value.settings.directional&&value.signals.length)return value.course==null?'進行方向を確認中':'進行方向に該当する信号なし'
 return picsStatusText(value)
})
let active=false,timer:ReturnType<typeof setTimeout>|undefined,request:AbortController|undefined

async function poll(){
 timer=undefined
 if(!active||document.hidden)return
 request=new AbortController()
 const timeout=setTimeout(()=>request?.abort(),1800)
 try{
  const next=await api<PicsStatus>('/pics','GET',undefined,true,request.signal)
  status.value=isPicsStatus(next)?next:undefined
 }
 catch{status.value=undefined}
 finally{clearTimeout(timeout);request=undefined}
 if(active)timer=setTimeout(poll,status.value?.settings.enabled?500:3000)
}
function followVisibility(){if(!document.hidden&&!timer&&!request)void poll()}
onMounted(()=>{active=true;document.addEventListener('visibilitychange',followVisibility);void poll()})
onUnmounted(()=>{active=false;clearTimeout(timer);request?.abort();document.removeEventListener('visibilitychange',followVisibility)})
</script>

<template>
 <a v-if="status?.settings.enabled" class="dashboard-signal" href="#system/pics" aria-label="高度化PICSを開く" :title="signal?.name||status.reason||'信号（高度化PICS）'">
  <span class="signal-dot" :class="signal?.color" aria-hidden="true"></span><span class="signal-label">信号</span>
  <span class="signal-value">{{text}}</span>
  <small v-if="signal&&signal.seconds!==null" class="signal-estimate">目安</small>
 </a>
</template>

<style scoped>
.dashboard-signal{display:flex;align-items:center;gap:6px;min-width:0;min-height:29px;padding:3px 10px;border-top:1px solid var(--line);background:var(--paper);color:var(--text);text-decoration:none;font-size:12px;line-height:1.3}
.signal-dot{width:7px;height:7px;flex:none;border-radius:50%;background:var(--muted)}
.signal-dot.red{background:#e05b54}.signal-dot.green,.signal-dot.blink-green{background:#328c68}
.signal-label{flex:none;font-weight:600}.signal-value{overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-variant-numeric:tabular-nums}.signal-estimate{flex:none;color:var(--muted);font-size:10px}
@media(max-width:600px){.dashboard-signal{padding-inline:6px;gap:4px;font-size:11px}}
</style>
