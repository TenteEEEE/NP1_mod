<script setup lang="ts">
import {computed,nextTick,onMounted,onUnmounted,ref,watch} from 'vue'
import {MapPin,RefreshCw} from '@lucide/vue'
import {api} from '../services/api'
import {jstDate} from '../services/journey'
import {picsColorText,picsCourseText,type PicsAxes,type PicsColor,type PicsHistory,type PicsReception,type PicsSignal} from '../services/pics'
import {VectorMap,collection,feature,type LatLon,type MapMouseEvent} from '../services/vector-map'

const props=defineProps<{live:PicsSignal[]}>()
const pageSize=60
const date=ref(jstDate()),intersection=ref(''),history=ref<PicsHistory>(),online=ref(true)
const container=ref<HTMLElement>(),list=ref<HTMLElement>(),mapFailure=ref<'background'|'map'>(),error=ref(''),loading=ref(false)
const selected=ref<number>(),visibleCount=ref(pageSize)
const rules=ref<Record<string,PicsAxes>>({}),axes=ref<(number|string|null)[]>([]),saving=ref(false),ruleNotice=ref(''),rulesLoaded=ref(false),rulesLoading=ref(false)
const timeFormat=new Intl.DateTimeFormat('ja-JP',{timeZone:'Asia/Tokyo',hour:'2-digit',minute:'2-digit',second:'2-digit'})
const dateFormat=new Intl.DateTimeFormat('ja-JP',{timeZone:'Asia/Tokyo',year:'numeric',month:'long',day:'numeric',weekday:'short'})
const dateLabel=computed(()=>date.value?dateFormat.format(new Date(`${date.value}T00:00:00+09:00`)):'')
let map:VectorMap|undefined,request:AbortController|undefined

function crossingLabel(record:PicsReception){return record.name||intersections.value.find(site=>site.id===record.id)?.name||record.id}

const intersections=computed(()=>{
 const groups=new Map<string,string>()
 for(const record of [...history.value?.entries??[],...props.live])if(record.name||!groups.has(record.id))groups.set(record.id,record.name||record.id)
 return [...groups].map(([id,name])=>({id,name}))
})
const entries=computed(()=>[...history.value?.entries??[]].reverse().filter(record=>!intersection.value||intersection.value===record.id))
const visible=computed(()=>entries.value.slice(0,visibleCount.value))
const points=computed(()=>entries.value.flatMap((record,index)=>record.position?[{point:record.position,record,index}]:[]))
const sites=computed(()=>{
 const byID=new Map<string,{index:number;record:PicsReception}>()
 for(const [index,record] of entries.value.entries())if(record.beacon&&!byID.has(record.id))byID.set(record.id,{index,record})
 return [...byID.values()]
})
const hasMapData=computed(()=>points.value.length>0||sites.value.length>0)
const numberText=(v:number)=>`${v.toLocaleString('ja-JP',{maximumFractionDigits:1})}秒`

function median(values:number[]){
 const sorted=[...values].sort((a,b)=>a-b),i=Math.floor(sorted.length/2)
 return sorted.length%2?sorted[i]!:(sorted[i-1]!+sorted[i]!)/2
}
const rhythms=computed(()=>{
 const groups=new Map<string,{label:string;cycles:number[];phases:Partial<Record<PicsColor,number[]>>}>()
 for(const row of entries.value){
  const key=`${row.id}-${row.slot}`
  let stats=groups.get(key)
  if(!stats){stats={label:`${crossingLabel(row)} 信号${row.slot}`,cycles:[],phases:{}};groups.set(key,stats)}
  if(row.cycleSeconds)stats.cycles.push(row.cycleSeconds)
  if(row.previousColor&&row.phaseSeconds)(stats.phases[row.previousColor]??=[]).push(row.phaseSeconds)
 }
 return [...groups].map(([key,stats])=>{
  const recent=stats.cycles.slice(0,5),estimate=recent.length===5&&Math.max(...recent)-Math.min(...recent)<=2?median(recent):undefined
  return {key,...stats,estimate,colorDurations:Object.entries(stats.phases).map(([color,values])=>`${picsColorText[color as PicsColor]} ${numberText(median(values))}（${values.length}回）`).join(' / ')}
 })
})

async function refresh(){
 request?.abort();error.value=''
 if(!date.value){loading.value=false;return}
 const current=new AbortController();request=current;loading.value=true
 try{
  const report=await api<PicsHistory>('/pics/history?'+new URLSearchParams({date:date.value}),'GET',undefined,true,current.signal)
  if(current.signal.aborted)return
  if(!Array.isArray(report.entries))throw new Error('受信記録を取得できません')
  history.value=report
  if(!intersections.value.some(item=>item.id===intersection.value))intersection.value=''
 }catch(e){if(!current.signal.aborted)error.value=e instanceof Error?e.message:'受信記録を取得できません'}
 finally{if(!current.signal.aborted)loading.value=false}
}

async function loadRules(){
 if(rulesLoaded.value||rulesLoading.value)return
 rulesLoading.value=true
 ruleNotice.value=''
 try{rules.value=await api<Record<string,PicsAxes>>('/pics/directions');rulesLoaded.value=true;loadAxes()}
 catch(e){ruleNotice.value=e instanceof Error?e.message:'横断方向を取得できません'}
 finally{rulesLoading.value=false}
}

function loadAxes(){
 axes.value=Array.from({length:6},(_,i)=>rules.value[intersection.value]?.[i]??null)
 ruleNotice.value=''
}
async function saveAxes(){
 const id=intersection.value
 saving.value=true;ruleNotice.value=''
 try{
  const saved=await api<PicsAxes>(`/pics/directions/${encodeURIComponent(id)}`,'PUT',{axes:axes.value.map(value=>value===''||value==null?null:Number(value))})
  rules.value[id]=saved
  if(intersection.value===id){loadAxes();ruleNotice.value='横断方向を保存しました'}
 }catch(e){ruleNotice.value=e instanceof Error?e.message:'横断方向を保存できません'}
 finally{saving.value=false}
}

function render(fit=false){
 if(!map)return
 const features=points.value.map(({point,index})=>feature({type:'Point',coordinates:[point.lon,point.lat]},{index,kind:'reception',selected:index===selected.value}))
 for(const {record,index} of sites.value){const point=record.beacon!;features.push(feature({type:'Point',coordinates:[point.lon,point.lat]},{index,kind:'beacon',selected:index===selected.value&&!record.position}))}
 map.setOverlay('pics-points',collection(features),[
  {id:'pics-beacon',type:'circle',source:'pics-points',filter:['==',['get','kind'],'beacon'],paint:{'circle-color':'#c77b20','circle-radius':['case',['get','selected'],11,8],'circle-stroke-color':'#fff','circle-stroke-width':2}},
  {id:'pics-location',type:'circle',source:'pics-points',filter:['==',['get','kind'],'reception'],paint:{'circle-color':'#306fca','circle-radius':['case',['get','selected'],10,5],'circle-stroke-color':'#fff','circle-stroke-width':['case',['get','selected'],3,1]}},
 ])
 if(fit)map.fit([
  ...points.value.map(({point}):LatLon=>[point.lat,point.lon]),
  ...sites.value.map(({record}):LatLon=>[record.beacon!.lat,record.beacon!.lon]),
 ])
}
function chooseRecord(index:number){
 selected.value=index
 const row=entries.value[index]
 const point=row?.position??row?.beacon
 if(point)map?.centre([point.lat,point.lon])
}
async function mapClick(event:MapMouseEvent){
 const marker=map?.features(event,['pics-location','pics-beacon'])[0]
 const index=Number(marker?.properties.index)
 if(!entries.value[index])return
 selected.value=index
 visibleCount.value=Math.max(visibleCount.value,(Math.floor(index/pageSize)+1)*pageSize)
 await nextTick()
 list.value?.querySelector<HTMLElement>(`[data-entry="${index}"]`)?.scrollIntoView({block:'nearest'})
}
watch(container,element=>{
 map?.destroy();map=undefined;mapFailure.value=undefined
 if(!element)return
 try{
  const current=new VectorMap({view:'journey',container:element,background:online.value,failed:()=>{
   if(map!==current||!online.value)return
   mapFailure.value='background'
   queueMicrotask(()=>{if(map===current)current.background(false)})
  }})
  map=current
  map.map.on('click',mapClick)
  render(true)
 }catch{mapFailure.value='map'}
})
watch(date,()=>{history.value=undefined;void refresh()})
watch(intersection,()=>{loadAxes();if(intersection.value)void loadRules()})
watch(entries,()=>{selected.value=undefined;visibleCount.value=pageSize;render(true)})
watch(selected,()=>render())
watch(online,value=>{if(mapFailure.value==='background')mapFailure.value=undefined;map?.background(value)})
onMounted(refresh)
onUnmounted(()=>{request?.abort();map?.destroy()})
</script>

<template>
 <section class="panel plate-records">
  <div class="plate-toolbar">
   <label>受信日（日本時間）<input v-model="date" type="date" required></label>
   <label class="pics-crossing"><span>交差点</span><select v-model="intersection"><option value="">すべて</option><option v-for="item in intersections" :key="item.id" :value="item.id">{{item.name}}</option></select></label>
   <label class="plate-check"><input v-model="online" type="checkbox">地図の背景を表示</label>
   <button type="button" class="secondary compact" :disabled="loading||!date" @click="refresh"><RefreshCw :size="14" aria-hidden="true"/>更新</button>
  </div>
  <p v-if="error" class="plate-list-error" role="alert">{{error}}</p>
  <p class="plate-count" role="status">
   {{loading?'読み込み中…':`${entries.length.toLocaleString()} 件（受信地点あり ${points.length.toLocaleString()} 件）`}}
   <span v-if="history?.incomplete"> · 保存上限または書き込みの問題により、一部の記録がありません</span>
  </p>
  <div class="plate-split">
   <div class="plate-map-wrap">
    <div class="plate-map-frame">
     <div v-if="hasMapData" ref="container" class="plate-map" role="region" aria-label="高度化PICSの受信地点地図"></div>
     <div v-else class="plate-map empty-map" aria-hidden="true"></div>
     <div v-if="!hasMapData" class="plate-map-placeholder"><MapPin :size="24" aria-hidden="true"/><strong>{{loading?'受信地点を読み込み中…':'地図に表示できる記録はありません'}}</strong></div>
    </div>
    <div v-if="hasMapData" class="plate-legend"><span><i class="dot gps-dot"></i>NP1の受信地点</span><span><i class="dot beacon-dot"></i>PICS広告の交差点位置</span></div>
    <p v-if="mapFailure" class="plate-map-empty" role="note">{{mapFailure==='background'?'背景地図を読み込めませんでした。受信地点は引き続き表示します。':'地図を表示できません。受信記録は一覧で確認できます。'}}</p>
   </div>
   <div ref="list" class="plate-list" role="region" aria-label="受信記録一覧">
    <h3 v-if="dateLabel" class="plate-day-heading"><time :datetime="date">{{dateLabel}}</time><span>{{entries.length.toLocaleString()}} 件</span></h3>
    <p v-if="!loading&&!entries.length&&!error" class="plate-empty">{{date?'この日の受信記録はありません。':'受信日を選んでください。'}}</p>
    <button v-for="(row,index) in visible" :key="`${row.id}-${row.slot}-${row.at}-${index}`" type="button" class="plate-item pics-entry" :class="{selected:index===selected}" :data-entry="index" :aria-pressed="index===selected" @click="chooseRecord(index)">
     <span class="pics-light" :class="row.color"><i aria-hidden="true"></i>{{picsColorText[row.color]}}</span>
     <span class="plate-item-info">
      <span class="pics-entry-title"><strong>{{crossingLabel(row)}}<small>信号{{row.slot}}</small></strong><time :datetime="row.at">{{timeFormat.format(new Date(row.at))}}</time></span>
      <span class="plate-meta">
       <strong v-if="row.estimatedToGreen">青まで約{{row.estimatedToGreen}}秒</strong>
       <span v-if="row.step">段階 {{row.step}}</span><span>{{picsCourseText(row.course)}}</span>
       <span v-if="!row.position" class="plate-nofix">受信地点なし</span>
      </span>
     </span>
    </button>
    <button v-if="entries.length>visibleCount" type="button" class="secondary more" @click="visibleCount+=pageSize">さらに表示</button>
   </div>
  </div>
  <details v-if="rhythms.length" class="pics-details">
   <summary>灯器ごとの観測周期と予測</summary>
   <div class="pics-rhythms">
    <article v-for="row in rhythms" :key="row.key">
     <h3>{{row.label}}</h3>
     <dl>
      <dt>一周の実測</dt><dd>{{row.cycles.length?`${numberText(median(row.cycles))}（${row.cycles.length}回）`:'未観測'}}</dd>
      <dt>予測周期</dt><dd>{{row.estimate===undefined?'未確定':`約${numberText(row.estimate)}`}}</dd>
      <dt>灯色別の実測中央値</dt><dd>{{row.colorDurations||'未観測'}}</dd>
     </dl>
    </article>
   </div>
  </details>
  <details v-if="intersection" class="pics-details directions">
   <summary>この交差点の横断方向を登録</summary>
   <form v-if="rulesLoaded" @submit.prevent="saveAxes">
    <p class="hint">横断方向を北0°、東90°、南180°、西270°で入力します。反対向きも同じ方向として照合します。対応が分からない欄は空欄にします。</p>
    <div class="axes"><label v-for="(_,index) in axes" :key="index">信号{{index+1}}の方位<input v-model="axes[index]" type="number" min="0" max="359.9" step="0.1" placeholder="未登録" :disabled="saving"></label></div>
    <button type="submit" class="secondary" :disabled="saving">横断方向を保存</button>
   </form>
   <p v-if="rulesLoading" class="hint">読み込み中…</p>
   <p v-if="ruleNotice" role="status" class="hint">{{ruleNotice}}</p>
   <button v-if="ruleNotice&&!rulesLoaded" type="button" class="secondary" @click="loadRules">再試行</button>
  </details>
 </section>
</template>

<style scoped>
.pics-crossing{flex:1 1 160px}.pics-crossing select{width:100%;min-height:44px}
.empty-map{height:clamp(150px,22vh,240px)}
.plate-legend{flex-wrap:wrap}.plate-legend .gps-dot,.plate-legend .beacon-dot{border-color:var(--paper);box-shadow:0 0 0 1px var(--line)}
.plate-legend .gps-dot{background:#306fca}.plate-legend .beacon-dot{background:#c77b20}
.pics-entry{text-align:left;min-height:64px;width:100%}.pics-entry.selected{outline-offset:-2px;background:var(--soft)}
.pics-entry:hover{background:var(--soft)}
.pics-entry-title{display:flex;align-items:baseline;justify-content:space-between;gap:6px 10px;flex-wrap:wrap}
.pics-entry-title strong{overflow-wrap:anywhere}.pics-entry-title small{margin-inline-start:8px;color:var(--muted);font-weight:500;white-space:nowrap}
.pics-entry-title time{font-size:12px;color:var(--muted);font-variant-numeric:tabular-nums}
.pics-light{--light:var(--accent);display:inline-flex;align-items:center;gap:5px;justify-content:center;justify-self:start;min-width:70px;padding:6px 8px;border-radius:6px;background:color-mix(in srgb,var(--light) 15%,var(--paper));font-size:12px;font-weight:650;white-space:nowrap}
.pics-light i{width:8px;height:8px;border-radius:50%;background:var(--light)}
.pics-light.red{--light:var(--danger)}.pics-light.green,.pics-light.blink-green{--light:#329779}.pics-light.blink-green i{background:transparent;border:2px solid var(--light)}
.pics-details{border-top:1px solid var(--line);margin-top:16px;padding-top:12px}
.pics-details summary{cursor:pointer;font-weight:650;font-size:13px}
.pics-rhythms{display:grid;grid-template-columns:repeat(auto-fill,minmax(min(100%,260px),1fr));gap:8px;margin-top:12px}
.pics-rhythms article{padding:10px 12px;border:1px solid var(--line);border-radius:8px;min-width:0}
.pics-rhythms h3{font-size:13px;overflow-wrap:anywhere}
.pics-rhythms dl{display:grid;grid-template-columns:auto minmax(0,1fr);gap:4px 8px;font-size:12px;margin:8px 0 0}
.pics-rhythms dt{color:var(--muted)}.pics-rhythms dd{margin:0;overflow-wrap:anywhere}
.directions form{display:grid;gap:12px;margin-top:12px}.directions form .hint{margin:0}
.axes{display:flex;flex-wrap:wrap;gap:10px}.axes label{display:grid;gap:4px}.axes input{width:7em}
.more,.directions button{justify-self:start}
@media(max-width:380px){.pics-light{min-width:57px;padding-inline:5px}}
</style>
