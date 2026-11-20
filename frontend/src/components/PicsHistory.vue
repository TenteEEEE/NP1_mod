<script setup lang="ts">
import {computed,onUnmounted,ref,watch} from 'vue'
import {api} from '../services/api'
import {jstDate} from '../services/journey'
import {picsColorText,picsCourseText,type PicsAxes,type PicsColor,type PicsHistory,type PicsReception,type PicsSignal} from '../services/pics'
import {VectorMap,collection,feature,type LatLon,type MapMouseEvent} from '../services/vector-map'

const props=defineProps<{live:PicsSignal[]}>()
const open=ref(false),date=ref(jstDate()),intersection=ref(''),history=ref<PicsHistory>()
const container=ref<HTMLElement>(),failed=ref(false),error=ref(''),loading=ref(false),selected=ref<PicsReception>(),visibleCount=ref(60)
const rules=ref<Record<string,PicsAxes>>({}),axes=ref<(number|string|null)[]>([]),saving=ref(false),ruleNotice=ref(''),rulesLoaded=ref(false)
const timeFormat=new Intl.DateTimeFormat('ja-JP',{timeZone:'Asia/Tokyo',hour:'2-digit',minute:'2-digit',second:'2-digit'})
let map:VectorMap|undefined,request:AbortController|undefined

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
  if(!stats){stats={label:`${row.name||row.id} 信号${row.slot}`,cycles:[],phases:{}};groups.set(key,stats)}
  if(row.cycleSeconds)stats.cycles.push(row.cycleSeconds)
  if(row.previousColor&&row.phaseSeconds)(stats.phases[row.previousColor]??=[]).push(row.phaseSeconds)
 }
 return [...groups.values()].map(stats=>{
  const recent=stats.cycles.slice(0,5),estimate=recent.length===5&&Math.max(...recent)-Math.min(...recent)<=2?median(recent):undefined
  return {...stats,estimate,colorDurations:Object.entries(stats.phases).map(([color,values])=>`${picsColorText[color as PicsColor]} ${numberText(median(values))}（${values.length}回）`).join(' / ')}
 })
})

async function refresh(){
 request?.abort();const current=new AbortController();request=current;loading.value=true;error.value=''
 try{
  const [report,directions]=await Promise.all([
   api<PicsHistory>('/pics/history?'+new URLSearchParams({date:date.value}),'GET',undefined,true,current.signal),
   rulesLoaded.value?Promise.resolve(undefined):api<Record<string,PicsAxes>>('/pics/directions','GET',undefined,true,current.signal),
  ])
  if(current.signal.aborted)return
  if(!Array.isArray(report.entries))throw new Error('受信記録を取得できません')
  history.value=report
  if(directions){rules.value=directions;rulesLoaded.value=true;loadAxes()}
  if(!intersections.value.some(item=>item.id===intersection.value))intersection.value=''
 }catch(e){if(!current.signal.aborted)error.value=e instanceof Error?e.message:'受信記録を取得できません'}
 finally{if(!current.signal.aborted)loading.value=false}
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

function render(){
 if(!map)return
 const features=points.value.map(({point,index})=>feature({type:'Point',coordinates:[point.lon,point.lat]},{index,kind:'reception'}))
 for(const {record,index} of sites.value){const point=record.beacon!;features.push(feature({type:'Point',coordinates:[point.lon,point.lat]},{index,kind:'beacon'}))}
 map.setOverlay('pics-points',collection(features),[
  {id:'pics-beacon',type:'circle',source:'pics-points',filter:['==',['get','kind'],'beacon'],paint:{'circle-color':'#c77b20','circle-radius':8,'circle-stroke-color':'#fff','circle-stroke-width':2}},
  {id:'pics-location',type:'circle',source:'pics-points',filter:['==',['get','kind'],'reception'],paint:{'circle-color':'#306fca','circle-radius':5,'circle-stroke-color':'#fff','circle-stroke-width':1}},
 ])
 map.fit(features.map(({geometry})=>{const {coordinates}=geometry as {type:'Point';coordinates:[number,number]};return [coordinates[1],coordinates[0]] as LatLon}))
}
function chooseRecord(row:PicsReception,centre=true){
 selected.value=row
 if(centre&&row.position)map?.centre([row.position.lat,row.position.lon])
}
function mapClick(event:MapMouseEvent){
 const point=map?.features(event,['pics-location','pics-beacon'])[0]
 const record=point&&entries.value[Number(point.properties.index)]
 if(record)chooseRecord(record,false)
}
watch(container,element=>{
 map?.destroy();map=undefined;failed.value=false
 if(!element)return
 try{map=new VectorMap({view:'journey',container:element,background:true,failed:()=>failed.value=true});map.map.on('click',mapClick);render()}
 catch{failed.value=true}
})
watch(open,value=>{if(value)void refresh();else request?.abort()})
watch(date,()=>{history.value=undefined;selected.value=undefined;if(open.value)void refresh()})
watch(intersection,()=>{selected.value=undefined;visibleCount.value=60;loadAxes()})
watch(entries,render)
onUnmounted(()=>{request?.abort();map?.destroy()})
</script>

<template>
 <details class="pics-history" @toggle="open=($event.currentTarget as HTMLDetailsElement).open">
  <summary>受信記録・地図・周期</summary>
  <div v-if="open" class="history-body">
   <div class="history-tools">
    <label>受信日（日本時間）<input v-model="date" type="date" required></label>
    <label>交差点<select v-model="intersection"><option value="">すべて</option><option v-for="item in intersections" :key="item.id" :value="item.id">{{item.name}}</option></select></label>
    <button type="button" class="secondary" :disabled="loading||!date" @click="refresh">{{loading?'取得中…':'更新'}}</button>
   </div>
   <p v-if="error" class="error" role="alert">{{error}}</p>
   <p v-if="history?.incomplete" class="hint">保存上限または書き込みの問題により、一部の記録がありません。</p>
   <div v-if="points.length||sites.length" ref="container" class="reception-map" role="region" aria-label="高度化PICSの受信地点地図"></div>
   <p v-if="entries.length" class="hint"><span class="gps-dot"></span>NP1の受信地点 {{points.length}}件 <span class="beacon-dot"></span>PICS広告の交差点位置 {{sites.length}}件 · 位置なし {{entries.length-points.length}}件</p>
   <p v-else-if="!loading&&history" class="hint">この日の受信記録はありません。</p>
   <p v-if="failed" class="hint">背景地図を表示できません。受信記録は下の一覧で確認できます。</p>
   <p v-if="selected" class="selected" role="status">{{timeFormat.format(new Date(selected.at))}} · {{selected.name||selected.id}} 信号{{selected.slot}} · {{picsColorText[selected.color]}} · {{picsCourseText(selected.course)}}</p>
   <div v-if="rhythms.length" class="history-table">
    <table><caption>灯器ごとの観測周期と予測</caption><thead><tr><th scope="col">交差点・灯器</th><th scope="col">一周の実測</th><th scope="col">予測周期</th><th scope="col">灯色別の実測中央値</th></tr></thead>
     <tbody><tr v-for="(row,index) in rhythms" :key="index"><td>{{row.label}}</td><td>{{row.cycles.length?`${numberText(median(row.cycles))}（${row.cycles.length}回）`:'未観測'}}</td><td>{{row.estimate===undefined?'未確定':`約${numberText(row.estimate)}`}}</td><td>{{row.colorDurations||'未観測'}}</td></tr></tbody>
    </table>
   </div>
   <details v-if="intersection&&rulesLoaded" class="directions">
    <summary>この交差点の横断方向を登録</summary>
    <form @submit.prevent="saveAxes">
     <p class="hint">横断歩道が延びる方向を、北0°、東90°、南180°、西270°で入力します。反対向きも同じ方向として照合します。対応が分からない欄は空欄にします。</p>
     <div class="axes"><label v-for="(_,index) in axes" :key="index">信号{{index+1}}の方位<input v-model="axes[index]" type="number" min="0" max="359.9" step="0.1" placeholder="未登録" :disabled="saving"></label></div>
     <button type="submit" class="secondary" :disabled="saving">横断方向を保存</button>
     <p v-if="ruleNotice" role="status" class="hint">{{ruleNotice}}</p>
    </form>
   </details>
   <div v-if="entries.length" class="history-table">
    <table><caption>受信の明細</caption><thead><tr><th scope="col">時刻</th><th scope="col">交差点・灯器</th><th scope="col">灯色</th><th scope="col">段階</th><th scope="col">受信時の予測</th><th scope="col">NP1の進行方向</th></tr></thead>
     <tbody><tr v-for="(row,index) in visible" :key="index"><td><button type="button" class="ghost" @click="chooseRecord(row)">{{timeFormat.format(new Date(row.at))}}</button></td><td>{{row.name||row.id}} 信号{{row.slot}}</td><td>{{picsColorText[row.color]}}</td><td>{{row.step??'—'}}</td><td>{{row.estimatedToGreen?`青まで約${row.estimatedToGreen}秒`:'—'}}</td><td>{{picsCourseText(row.course)}}</td></tr></tbody>
    </table>
   </div>
   <button v-if="entries.length>visibleCount" type="button" class="secondary more" @click="visibleCount+=60">さらに表示</button>
  </div>
 </details>
</template>

<style scoped>
.pics-history{border-top:1px solid var(--line);padding-top:12px}.pics-history summary{cursor:pointer;font-weight:650}.history-body,.directions form{display:grid;gap:12px;margin-top:12px}.history-tools,.axes{display:flex;flex-wrap:wrap;align-items:end;gap:10px}.history-tools label,.axes label{display:grid;gap:4px}.axes input{width:7em}.reception-map{height:360px;border:1px solid var(--line);border-radius:10px;overflow:hidden}.gps-dot,.beacon-dot{display:inline-block;width:9px;height:9px;border-radius:50%;margin:0 5px 0 3px;background:#306fca}.beacon-dot{background:#c77b20}.selected{background:var(--soft);padding:8px;border-radius:6px}.history-table{overflow-x:auto}.history-table table{width:100%;border-collapse:collapse;text-align:left;font-size:.9em}.history-table caption{text-align:left;font-weight:650;margin-bottom:6px}.history-table th,.history-table td{padding:6px;border-bottom:1px solid var(--line);white-space:nowrap}.history-table td button{padding:2px 5px}.more,.directions button{justify-self:start}@media(max-width:600px){.reception-map{height:270px}}
</style>
