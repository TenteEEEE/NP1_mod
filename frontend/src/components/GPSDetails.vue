<script setup lang="ts">
import {computed,onMounted,onUnmounted,ref} from 'vue'
import {Crosshair,RefreshCw,Satellite} from '@lucide/vue'
import CurrentLocation from './CurrentLocation.vue'
import {api,type Observation} from '../services/api'

type GPS={fix?:boolean;live?:boolean;at?:string;lat?:number;lon?:number;satellites?:number;accuracyM?:number;hdop?:number;qualityAgeSeconds?:number;positionSource?:string;lastKnown?:{at?:string;lat?:number;lon?:number}}
const observation=ref<Observation<GPS>>(),loading=ref(false),error=ref(''),canvas=ref<HTMLCanvasElement>()
const gps=computed(()=>observation.value?.data)
const finite=(value:unknown):value is number=>typeof value==='number'&&Number.isFinite(value)
const satellites=computed(()=>finite(gps.value?.satellites)?Math.max(0,Math.round(gps.value!.satellites!)):0)
const timestamp=computed(()=>{const at=Date.parse(gps.value?.at||'');return Number.isFinite(at)?at:undefined})
const age=computed(()=>timestamp.value===undefined?undefined:Math.max(0,Math.round((Date.now()-timestamp.value)/1000)))
const ageText=computed(()=>age.value===undefined?'—':age.value<60?`${age.value}秒前`:`${Math.floor(age.value/60)}分前`)
const qualityAge=computed(()=>finite(gps.value?.qualityAgeSeconds)?gps.value!.qualityAgeSeconds!:undefined)
const qualityAgeText=computed(()=>qualityAge.value===undefined?'更新時刻不明':qualityAge.value<60?`${qualityAge.value}秒前の品質`:`${Math.floor(qualityAge.value/60)}分前の品質`)
const state=computed(()=>observation.value?.status!=='available'?'データ待ち':gps.value?.fix?'測位中':'測位待ち')
const source=computed(()=>gps.value?.positionSource||observation.value?.source||'—')
const shown=(value:unknown,suffix='')=>finite(value)?`${value}${suffix}`:'—'
let timer:ReturnType<typeof setInterval>|undefined,frame=0,spin=0,mounted=false

async function load(){loading.value=true;try{error.value='';observation.value=await api<Observation<GPS>>('/device/gps/live')}catch(e){error.value=e instanceof Error?e.message:String(e)}finally{loading.value=false}}
function visibility(){if(!document.hidden)void load()}
function draw(){
 if(!mounted)return
 const el=canvas.value
 if(!el){frame=requestAnimationFrame(draw);return}
 const dpr=Math.min(window.devicePixelRatio||1,2),box=el.getBoundingClientRect(),w=Math.max(1,box.width),h=Math.max(1,box.height)
 if(el.width!==Math.round(w*dpr)||el.height!==Math.round(h*dpr)){el.width=Math.round(w*dpr);el.height=Math.round(h*dpr)}
 const c=el.getContext('2d');if(!c)return
 c.setTransform(dpr,0,0,dpr,0,0);c.clearRect(0,0,w,h)
 const css=getComputedStyle(el),line=css.getPropertyValue('--line-strong').trim()||'#64748b',accent=css.getPropertyValue('--accent').trim()||'#2563eb',text=css.getPropertyValue('--muted').trim()||'#64748b',paper=css.getPropertyValue('--paper').trim()||'#fff'
 const cx=w/2,cy=h*.51,r=Math.min(w*.27,h*.31)
 const glow=c.createRadialGradient(cx-r*.3,cy-r*.4,r*.08,cx,cy,r*1.35);glow.addColorStop(0,accent+'33');glow.addColorStop(.65,accent+'12');glow.addColorStop(1,'transparent');c.fillStyle=glow;c.fillRect(0,0,w,h)
 c.save();c.beginPath();c.arc(cx,cy,r,0,Math.PI*2);c.clip();const earth=c.createLinearGradient(cx-r,cy-r,cx+r,cy+r);earth.addColorStop(0,paper);earth.addColorStop(1,accent+'24');c.fillStyle=earth;c.fillRect(cx-r,cy-r,r*2,r*2);c.strokeStyle=line;c.globalAlpha=.55;c.lineWidth=1
 for(const k of [-.66,-.33,0,.33,.66]){c.beginPath();c.ellipse(cx,cy,r*Math.sqrt(1-k*k),r*.34,0,0,Math.PI*2);c.stroke()}
 for(let k=0;k<6;k++){c.beginPath();c.ellipse(cx,cy,r*.34,r,Math.PI*k/6,0,Math.PI*2);c.stroke()}
 c.restore();c.globalAlpha=1;c.strokeStyle=line;c.lineWidth=1.25;c.beginPath();c.arc(cx,cy,r,0,Math.PI*2);c.stroke()
 const count=Math.min(satellites.value,32)
 for(let i=0;i<count;i++){const ring=i%3,phase=spin+i*(Math.PI*2/Math.max(1,count)),rx=r*(1.3+ring*.18),ry=r*(.42+ring*.1),tilt=(ring-1)*.38,x=cx+Math.cos(phase)*rx,y=cy+Math.sin(phase)*ry+Math.cos(phase)*tilt*r;c.fillStyle=accent;c.beginPath();c.arc(x,y,3.4,0,Math.PI*2);c.fill();c.strokeStyle=paper;c.lineWidth=1.5;c.stroke()}
 c.fillStyle=text;c.font='12px system-ui';c.textAlign='center';c.fillText(count?`${satellites.value}衛星を受信`:'衛星を探索中',cx,h-22)
 if(!matchMedia('(prefers-reduced-motion: reduce)').matches)spin=(spin+.0035)%(Math.PI*2)
 frame=requestAnimationFrame(draw)
}
onMounted(()=>{mounted=true;void load();timer=setInterval(()=>{if(!document.hidden)void load()},5000);document.addEventListener('visibilitychange',visibility);draw()})
onUnmounted(()=>{mounted=false;if(timer)clearInterval(timer);cancelAnimationFrame(frame);document.removeEventListener('visibilitychange',visibility)})
</script>

<template>
 <div class="gps-page">
  <section class="panel gps-summary">
   <div class="panel-head"><h2><Crosshair :size="18"/>GPS詳細</h2><div class="gps-state"><i :class="{live:gps?.fix}"></i>{{state}}<button class="text-button" :disabled="loading" @click="load"><RefreshCw :size="14"/>更新</button></div></div>
   <div v-if="error" class="gps-error" role="alert">{{error}}</div>
   <div class="gps-kpis"><div><span>受信衛星</span><strong>{{satellites||'—'}}</strong><small>{{qualityAgeText}}</small></div><div><span>位置精度</span><strong>{{shown(gps?.accuracyM,' m')}}</strong><small>HDOP {{shown(gps?.hdop)}}</small></div><div><span>位置の更新</span><strong>{{ageText}}</strong><small>{{gps?.live?'リアルタイム':'保存された測位'}}</small></div><div><span>取得元</span><strong class="gps-source">{{source}}</strong><small>{{observation?.verified?'検証済み':'本体の観測値'}}</small></div></div>
  </section>
  <div class="gps-grid">
   <section class="panel gps-map"><div class="panel-head"><h2>現在地</h2><span class="muted">本体GPSと走行軌跡</span></div><CurrentLocation :observation="observation"/><dl class="gps-values"><div><dt>緯度</dt><dd>{{shown(gps?.lat)}}</dd></div><div><dt>経度</dt><dd>{{shown(gps?.lon)}}</dd></div><div><dt>測位状態</dt><dd>{{gps?.fix?'有効':'未測位'}}</dd></div><div><dt>品質の更新</dt><dd>{{qualityAgeText}}</dd></div></dl></section>
   <section class="panel gps-sky"><div class="panel-head"><h2><Satellite :size="18"/>衛星ビュー</h2><span class="muted">ブラウザ内で描画</span></div><canvas ref="canvas" aria-label="受信衛星数を示す3D地球儀"></canvas><p class="gps-sky-note">受信衛星数を地球儀上に可視化した模式図です。点の位置は実際の衛星軌道・方位を表しません。</p></section>
  </div>
 </div>
</template>

<style scoped>
.gps-page{display:grid;gap:20px}.panel-head h2,.gps-state{display:flex;align-items:center;gap:9px}.gps-state{font-size:12px;color:var(--muted)}.gps-state i{width:8px;height:8px;border-radius:50%;background:var(--line-strong)}.gps-state i.live{background:#22c55e;box-shadow:0 0 0 4px #22c55e20}.gps-error{padding:12px 20px;border-top:1px solid var(--line);background:color-mix(in srgb,#dc2626 8%,var(--paper));color:#b91c1c;font-size:12px}.gps-kpis{display:grid;grid-template-columns:repeat(4,minmax(0,1fr))}.gps-kpis>div{padding:20px 22px;border-right:1px solid var(--line);display:grid;gap:7px;min-width:0}.gps-kpis>div:last-child{border:0}.gps-kpis span,.gps-kpis small{font-size:11px;color:var(--muted)}.gps-kpis strong{font-size:20px;font-variant-numeric:tabular-nums}.gps-kpis .gps-source{font-size:13px;overflow-wrap:anywhere}.gps-grid{display:grid;grid-template-columns:minmax(0,1.2fr) minmax(340px,.8fr);gap:20px}.gps-map{overflow:hidden}.gps-values{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));margin:0;border-top:1px solid var(--line)}.gps-values>div{padding:15px 18px;border-right:1px solid var(--line)}.gps-values>div:last-child{border:0}.gps-values dt{font-size:11px;color:var(--muted)}.gps-values dd{margin:5px 0 0;font-size:13px;font-variant-numeric:tabular-nums;overflow-wrap:anywhere}.gps-sky{overflow:hidden}.gps-sky canvas{display:block;width:100%;height:340px;background:radial-gradient(circle at 50% 50%,color-mix(in srgb,var(--accent) 8%,transparent),transparent 68%)}.gps-sky-note{margin:0;padding:13px 18px;border-top:1px solid var(--line);color:var(--muted);font-size:11px;line-height:1.65}@media(max-width:900px){.gps-grid{grid-template-columns:1fr}.gps-kpis{grid-template-columns:repeat(2,minmax(0,1fr))}.gps-kpis>div:nth-child(2){border-right:0}.gps-kpis>div:nth-child(-n+2){border-bottom:1px solid var(--line)}}@media(max-width:520px){.gps-page{gap:14px}.gps-kpis{grid-template-columns:1fr}.gps-kpis>div{border-right:0;border-bottom:1px solid var(--line)}.gps-kpis>div:last-child{border-bottom:0}.gps-values{grid-template-columns:repeat(2,minmax(0,1fr))}.gps-values>div:nth-child(2){border-right:0}.gps-values>div:nth-child(-n+2){border-bottom:1px solid var(--line)}.gps-sky canvas{height:280px}}
</style>
