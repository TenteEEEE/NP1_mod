<script setup lang="ts">
import {computed,onMounted,onUnmounted,ref,watch} from 'vue'
import {Navigation,MapPin} from '@lucide/vue'
import {VectorMap,Marker,mapAttribution,collection,feature,circleGeometry,routeLayers,locationElement,type LatLon} from '../services/vector-map'
import '../radio-map.css'
import {api,type Observation} from '../services/api'
import {recentTrail} from '../services/trail'

type Position={fix?:boolean;live?:boolean;at?:string;lat?:number;lon?:number;satellites?:number;accuracyM?:number;qualityAgeSeconds?:number;lastKnown?:{at?:string;lat?:number;lon?:number}}
// `inset` is the part of a hero map hidden behind other panels, in pixels from each edge.
const props=defineProps<{observation?:Observation<Position>;hero?:boolean;inset?:{top:number;right:number;bottom:number;left:number}}>()
const trail=ref<[number,number][][]>([])
let trailTimer:ReturnType<typeof setInterval>|undefined
async function loadTrail(){try{const result=await recentTrail(24);if(mounted)trail.value=result}catch{/* retain the last successful track */}}
function visibilityChanged(){if(!document.hidden)void loadTrail()}
// The map follows the per-second position; the summary passed in (refreshed less often) is the fallback.
const live=ref<Observation<Position>>()
let liveTimer:ReturnType<typeof setInterval>|undefined
async function loadLive(){try{live.value=await api<Observation<Position>>('/device/gps/live')}catch{live.value=undefined}}
// Only a well-formed observation replaces the summary (an error page or {} must not hide it).
const observation=computed(()=>typeof live.value?.status==='string'?live.value:props.observation)
// The position is the stock per-second position while it is backed by a fix; otherwise the
// recorder's NMEA (written when a clip closes, about every 3 minutes) up to ~10 minutes old,
// always with its age. Satellites and accuracy come from NMEA and show their own age.
const MAX_AGE=11*60*1000
const container=ref<HTMLElement>(),now=ref(Date.now()),tileFailed=ref(false),following=ref(true)
let zoom=15,markerLive=false,map:VectorMap|undefined,marker:Marker|undefined,resize:ResizeObserver|undefined,timer:ReturnType<typeof setInterval>|undefined,mounted=false
const finite=(value:unknown):value is number=>typeof value==='number'&&Number.isFinite(value)
const current=computed(()=>{
 const gps=observation.value?.data,at=Date.parse(gps?.at||'')
 if(observation.value?.status!=='available'||gps?.fix!==true||!finite(gps.lat)||!finite(gps.lon)||Math.abs(gps.lat)>90||Math.abs(gps.lon)>180||!Number.isFinite(at)||now.value-at>MAX_AGE||at-now.value>60000)return undefined
 return gps
})
const age=computed(()=>current.value?Math.max(0,Math.round((now.value-Date.parse(current.value.at!))/1000)):0)
const ageText=computed(()=>!current.value||age.value<=20?'':age.value<60?`${age.value}秒前`:`${Math.floor(age.value/60)}分前`)
const lastKnown=computed<[number,number]|undefined>(()=>{const k=observation.value?.data?.lastKnown;return k&&finite(k.lat)&&finite(k.lon)&&Math.abs(k.lat)<=90&&Math.abs(k.lon)<=180?[k.lat,k.lon]:undefined})
const lastTrail=computed<[number,number]|undefined>(()=>{if(lastKnown.value)return lastKnown.value;const segments=trail.value;for(let i=segments.length-1;i>=0;i--){const seg=segments[i]!;if(seg.length)return seg[seg.length-1]}return undefined})
const lastLabel=computed(()=>lastKnown.value?'最後に分かっている位置':'最後の記録位置')
const quality=computed(()=>{const d=current.value;if(!d)return {sats:'—',acc:'—'};const a=d.qualityAgeSeconds,suffix=finite(a)&&a>20?(a<60?`（${a}秒前）`:`（${Math.floor(a/60)}分前）`):'';return {sats:finite(d.satellites)?`${d.satellites}${suffix}`:'—',acc:finite(d.accuracyM)&&d.accuracyM>0?`${Math.round(d.accuracyM)} m${suffix}`:'—'}})
const shown=computed(()=>!!current.value||!!lastTrail.value)
const label=computed(()=>current.value?'現在地':observation.value?.data?.fix===false?'測位待ち':observation.value?.data?.fix===true?'現在地を更新できません':'位置情報を取得できません')
const insetStyle=computed(()=>props.inset?{'--hero-pad-top':props.inset.top+'px','--hero-pad-right':props.inset.right+'px','--hero-pad-bottom':props.inset.bottom+'px','--hero-pad-left':props.inset.left+'px'}:undefined)
// A hero map is drawn larger than its frame, past the edge opposite the wider covered side, so that the
// middle of the drawing is the middle of the uncovered part. The position then stays put while zooming.
const canvasStyle=computed(()=>{
 const hidden=props.hero?props.inset:undefined;if(!hidden)return undefined
 const x=hidden.left-hidden.right,y=hidden.top-hidden.bottom
 return {left:Math.min(0,x)+'px',right:Math.min(0,-x)+'px',top:Math.min(0,y)+'px',bottom:Math.min(0,-y)+'px'}
})
function centre(point:LatLon){map?.centre(point)}
function resume(){following.value=true;const point=anchor();if(point)centre(point)}
function clear(){resize?.disconnect();resize=undefined;map?.destroy();map=undefined;marker=undefined}
function anchor():LatLon|undefined{const gps=current.value;return gps?[gps.lat!,gps.lon!]:lastTrail.value}
function drawTrail(){if(!map)return;map.setOverlay('current-trail',collection(trail.value.map((seg,index)=>feature(seg.length>1?{type:'LineString',coordinates:seg.map(p=>[p[1],p[0]])}:{type:'Point',coordinates:[seg[0]![1],seg[0]![0]]},{segment:index,count:seg.length}))),routeLayers('current-trail','#2f5bd8',4))}
function update(){
 if(!mounted||!container.value)return
 const gps=current.value,point=anchor()
 if(!point){clear();return}
 if(!map){
  tileFailed.value=false
  // Dragging suspends following until the user explicitly returns to the vehicle.
  try{map=new VectorMap({view:'dashboard',container:container.value,hero:props.hero,background:true,center:[point[1],point[0]],zoom:props.hero?zoom:15,scrollZoom:props.hero,failed:()=>tileFailed.value=true})}catch{tileFailed.value=true;return}
  map.map.on('dragstart',()=>following.value=false)
  if(props.hero)map.map.on('zoomend',()=>{if(map)zoom=map.map.getZoom()})
  resize=new ResizeObserver(()=>{const p=anchor();if(p&&following.value)centre(p)});resize.observe(container.value)
  drawTrail()
 }
 // The marker is the newest recorded position; without one, the end of the trail is shown greyed.
 if(marker&&markerLive!==!!gps){marker.remove();marker=undefined}
 markerLive=!!gps
 if(!marker){const element=locationElement(gps?'current-location-marker':'current-location-last',!!gps);element.title=gps?'現在地':lastLabel.value;marker=new Marker({element}).setLngLat([point[1],point[0]]).addTo(map.map)}
 else marker.setLngLat([point[1],point[0]])
 map.setOverlay('gps-accuracy',collection(gps&&finite(gps.accuracyM)&&gps.accuracyM>0?[feature(circleGeometry(gps.lat!,gps.lon!,gps.accuracyM))]:[]),[{id:'gps-accuracy-area',type:'fill',source:'gps-accuracy',paint:{'fill-color':'#397fd7','fill-opacity':.1}},{id:'gps-accuracy-edge',type:'line',source:'gps-accuracy',paint:{'line-color':'#397fd7','line-opacity':.4,'line-width':1}}])
 if(following.value)centre(point)
}
watch([current,lastTrail],update,{flush:'post'})
watch(trail,()=>{if(map)drawTrail();else update()},{flush:'post'})
onMounted(()=>{mounted=true;timer=setInterval(()=>now.value=Date.now(),1000);update();loadLive();liveTimer=setInterval(()=>{if(!document.hidden)loadLive()},2000);void loadTrail();trailTimer=setInterval(()=>{if(!document.hidden)void loadTrail()},60000);document.addEventListener('visibilitychange',visibilityChanged)})
onUnmounted(()=>{mounted=false;if(timer)clearInterval(timer);if(liveTimer)clearInterval(liveTimer);clearInterval(trailTimer);document.removeEventListener('visibilitychange',visibilityChanged);clear()})
</script>

<template>
 <div class="current-location radio-survey" :class="{hero}" :style="insetStyle" role="region" aria-label="現在地の地図">
  <div class="current-location-frame">
   <div ref="container" class="current-location-canvas radio-map-canvas" :class="{unavailable:!shown}" :style="canvasStyle" aria-label="現在地を示す地図"></div>
   <span v-if="current" class="current-location-label"><i></i>現在地</span>
   <button v-if="shown" class="current-location-resume icon-button" :aria-label="current?'現在地に戻る':'最後の位置に戻る'" :title="current?'現在地に戻る':'最後の位置に戻る'" :aria-pressed="following" @click="resume"><Navigation :size="20" :stroke-width="1.5" fill="currentColor"/></button>
   <div v-else-if="!shown" class="current-location-empty"><MapPin :size="28" stroke-width="1.5"/><strong>{{label}}</strong><p>測位すると、ここに現在地の地図を表示します。</p></div>
   <!-- On a hero map the readings sit at the bottom left and the map credit at the bottom right, on one line. -->
   <div class="current-location-foot">
   <div class="current-location-details">
    <div class="current-location-coordinates" aria-label="現在地の緯度経度"><span v-if="hero">{{current?`${current.lat!.toFixed(6)}, ${current.lon!.toFixed(6)}`:lastKnown?`${lastLabel} ${lastKnown[0].toFixed(6)}, ${lastKnown[1].toFixed(6)}`:lastTrail?lastLabel:'—'}}</span><template v-else><span>緯度 {{current?.lat?.toFixed(6)??'—'}}</span><span>経度 {{current?.lon?.toFixed(6)??'—'}}</span></template></div>
    <div class="current-location-quality"><span>衛星 {{quality.sats}}</span><span>精度 {{quality.acc}}</span><span v-if="!current&&observation?.data?.fix===false" class="current-location-age">測位できていません</span><span v-if="ageText" class="current-location-age">{{ageText}}の位置</span><span v-if="current&&!hero" class="current-location-time">{{new Date(current.at!).toLocaleTimeString('ja-JP',{hour:'2-digit',minute:'2-digit',second:'2-digit'})}}</span></div>
   </div>
   <span v-if="hero&&shown" class="current-location-credit" v-html="mapAttribution"></span>
   </div>
   <p v-if="shown&&tileFailed" class="current-location-note">背景地図を取得できません</p>
  </div>
 </div>
</template>

<style scoped>
.current-location.radio-survey{padding:0}.current-location-frame{height:276px;border-block:1px solid var(--line);position:relative;overflow:hidden}.current-location-canvas.radio-map-canvas{width:100%;height:100%;min-height:0}.current-location-canvas.unavailable{visibility:hidden}.current-location-label{position:absolute;top:12px;left:58px;z-index:450;display:flex;align-items:center;gap:7px;padding:7px 10px;border:1px solid var(--line);border-radius:5px;background:var(--paper);color:var(--text);font-size:12px;font-weight:650;box-shadow:0 1px 6px #00000018;pointer-events:none}.current-location-label i{width:7px;height:7px;border-radius:50%;background:var(--accent)}.current-location-empty{position:absolute;inset:0;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:10px;padding:18px 18px 80px;text-align:center;background-color:var(--bg);background-image:linear-gradient(var(--line) 1px,transparent 1px),linear-gradient(90deg,var(--line) 1px,transparent 1px);background-size:44px 44px;color:var(--muted)}.current-location-empty strong{color:var(--text);font-size:15px;font-weight:600}.current-location-empty p{font-size:12px;line-height:1.7;margin:0}.current-location-details{position:absolute;left:12px;right:12px;bottom:24px;z-index:450;display:flex;flex-wrap:wrap;justify-content:space-between;gap:6px 16px;padding:9px 11px;border:1px solid var(--line);border-radius:6px;background:color-mix(in srgb,var(--paper) 94%,transparent);color:var(--text);font-size:11px;line-height:1.7;font-variant-numeric:tabular-nums;box-shadow:0 1px 6px #00000018;pointer-events:none}.current-location-coordinates,.current-location-quality{display:flex;gap:6px 14px;flex-wrap:wrap}.current-location-coordinates span,.current-location-quality span{white-space:nowrap}.current-location-quality{color:var(--muted)}.current-location-time{margin-left:auto}.current-location-note{position:absolute;top:12px;right:12px;z-index:450;max-width:calc(100% - 170px);padding:6px 9px;border:1px solid var(--line);border-radius:5px;background:var(--paper);font-size:11px;line-height:1.7;color:var(--muted);margin:0;pointer-events:none}.current-location.hero{position:absolute;inset:0}.hero .current-location-frame{height:100%;border:0}.hero .current-location-label{display:none}.hero .current-location-empty{padding:calc(var(--hero-pad-top,0px) + 18px) calc(var(--hero-pad-right,0px) + 18px) calc(var(--hero-pad-bottom,0px) + 18px) calc(var(--hero-pad-left,0px) + 18px)}.hero .current-location-canvas.radio-map-canvas{position:absolute;inset:0;width:auto;height:auto}.current-location-foot{display:contents}.hero .current-location-foot{position:absolute;left:calc(16px - var(--hero-credit-lift,0)*8px);right:12px;bottom:var(--dh-foot,calc(4px + var(--hero-credit-lift,0)*(var(--hero-pad-bottom,0px) + 2px)));z-index:450;display:flex;align-items:flex-end;justify-content:space-between;gap:8px;pointer-events:none}.hero .current-location-details,.hero .current-location-credit{border:0;border-radius:6px;padding:1px 8px;background:color-mix(in srgb,var(--paper) 62%,transparent);backdrop-filter:blur(6px) saturate(1.3);-webkit-backdrop-filter:blur(6px) saturate(1.3);box-shadow:none;font-size:11px;line-height:1.6}.hero .current-location-details{position:static;flex:0 1 auto;min-width:0;justify-content:flex-start;gap:0 12px}.hero .current-location-credit{flex:none;margin-left:auto;color:var(--muted);pointer-events:auto}.hero .current-location-credit :deep(a){color:var(--accent)}.hero .current-location-quality{width:auto;color:var(--text);opacity:.72}.current-location-age{color:#b45309;font-weight:650}.hero .current-location-note{top:calc(var(--hero-pad-top,0px) + 12px);right:calc(var(--hero-pad-right,0px) + 12px)}
@media(max-width:520px){.current-location-frame{height:260px}.current-location-empty p{max-width:220px}.current-location-details{gap:3px 12px}.current-location-quality{width:100%}}
</style>
