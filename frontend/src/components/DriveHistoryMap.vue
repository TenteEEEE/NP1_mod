<script setup lang="ts">
import {computed,onMounted,onUnmounted,ref,watch} from 'vue'
import L from 'leaflet'
import {mapTiles} from '../services/map-tiles'
import {api,type Observation} from '../services/api'
import {jstDate} from '../services/journey'
import type {PlaybackTrack,TrackPoint} from '../services/playbackTrack'
import 'leaflet/dist/leaflet.css'
import '../radio-map.css'

type Point={at:string;gps:{at:string;lat:number;lon:number;accuracyM?:number};value:number|null;state:string;color:string;carrier?:string;band?:string;source:string;accuracyUnknown:boolean}
type Cell={bounds:[[number,number],[number,number]];color:string;median:number;sample_count:number;distinct_trip_count:number;low_confidence:boolean}
type History={tripWeighted?:boolean;tripCountKnown?:boolean;unit:string;points:Point[];cells:Cell[];total:number;unplaced:number;accuracyExcluded:number;missing:number;outOfService:number;carriers:string[];bands:string[];truncated:boolean}
type Legend={unit:string;note:string;thresholds:{minimum:number|null;label:string;color:string}[]}
const props=defineProps<{date:string}>()
const day=computed(()=>props.date||jstDate())
const container=ref<HTMLElement>(),overlay=ref(false),online=ref(true),metric=ref('rsrp'),mode=ref('grid'),carrier=ref(''),band=ref(''),size=ref('50'),unknown=ref(true)
const track=ref<PlaybackTrack>(),result=ref<History>(),legend=ref<Legend>(),loading=ref(false),radioLoading=ref(false),error=ref(''),radioError=ref(''),tileFailed=ref(false),selected=ref<Point>()
let map:L.Map|undefined,routeLayer:L.LayerGroup|undefined,radioLayer:L.LayerGroup|undefined,tiles:L.TileLayer|undefined,resize:ResizeObserver|undefined,timer:ReturnType<typeof setInterval>|undefined
let request:AbortController|undefined,radioRequest:AbortController|undefined,sequence=0,radioSequence=0,fitted=false,active=true
const overlayPoints=computed(()=>!overlay.value?[]:mode.value==='points'||!result.value?.cells.length?result.value?.points||[]:(result.value?.points||[]).filter(point=>point.accuracyUnknown))
const empty=computed(()=>!track.value?.points.length&&!overlayPoints.value.length&&(!overlay.value||mode.value!=='grid'||!result.value?.cells.length))
const fmt=(at:string)=>new Date(at).toLocaleString('ja-JP',{timeZone:'Asia/Tokyo',month:'numeric',day:'numeric',hour:'2-digit',minute:'2-digit',second:'2-digit'})
function popup(lines:string[]){const node=document.createElement('div');for(const text of lines){const p=document.createElement('div');p.textContent=text;node.appendChild(p)}return node}
async function loadTrack(){
 const id=++sequence;request?.abort();const current=new AbortController();request=current;loading.value=true;error.value=''
 if(track.value?.date!==day.value){track.value=undefined;fitted=false;render()}
 try{const value=await api<Observation<PlaybackTrack>>('/recordings/track?'+new URLSearchParams({date:day.value}),'GET',undefined,true,current.signal)
  if(id!==sequence||!active)return
  if(!Array.isArray(value.data?.points)||!Array.isArray(value.data?.segments))throw new Error('走行経路を取得できません')
  track.value=value.data;render()
 }catch(e){if(id===sequence&&!current.signal.aborted)error.value=e instanceof Error?e.message:'走行経路を取得できません'}
 finally{if(id===sequence)loading.value=false}
}
async function loadRadio(){
 const id=++radioSequence;radioRequest?.abort();result.value=undefined;selected.value=undefined;radioError.value='';render()
 if(!overlay.value){radioLoading.value=false;return}
 const current=new AbortController();radioRequest=current;radioLoading.value=true
 const query=new URLSearchParams({date:day.value,metric:metric.value,cellSize:size.value,includeUnknownAccuracy:String(unknown.value),tripWeighted:'true'})
 if(carrier.value)query.set('carrier',carrier.value);if(band.value)query.set('band',band.value)
 try{const [history,scale]=await Promise.all([api<History>('/radio/history?'+query,'GET',undefined,true,current.signal),api<Legend>('/radio/heatmap/legend?metric='+metric.value,'GET',undefined,true,current.signal)])
  if(id!==radioSequence||!active)return
  if(!Array.isArray(history.points)||!Array.isArray(history.cells))throw new Error('電波履歴を取得できません')
  result.value=history;legend.value=scale;render()
 }catch(e){if(id===radioSequence&&!current.signal.aborted)radioError.value=e instanceof Error?e.message:'電波履歴を取得できません'}
 finally{if(id===radioSequence)radioLoading.value=false}
}
function bounds(){
 const points:L.LatLngTuple[]=(track.value?.points||[]).map(p=>[p.lat,p.lon])
 if(overlay.value){if(mode.value==='grid')for(const cell of result.value?.cells||[])points.push(...cell.bounds);for(const p of overlayPoints.value)points.push([p.gps.lat,p.gps.lon])}
 return points
}
function fit(){const points=bounds();if(map&&points.length){map.fitBounds(L.latLngBounds(points),{padding:[28,28],maxZoom:17,animate:false});fitted=true}}
function render(){
 if(!map||!routeLayer||!radioLayer)return
 routeLayer.clearLayers();radioLayer.clearLayers()
 const groups=new Map<number,TrackPoint[]>()
 for(const point of track.value?.points||[]){const group=groups.get(point.segment)||[];group.push(point);groups.set(point.segment,group)}
 for(const points of groups.values()){
  const path=points.length>1?L.polyline(points.map(p=>[p.lat,p.lon]),{color:'#397fd7',weight:4,opacity:.9,className:'journey-route'}):L.circleMarker([points[0]!.lat,points[0]!.lon],{radius:4,color:'#397fd7',className:'journey-route'})
  path.bindPopup(popup([fmt(points[0]!.at)+' — '+fmt(points[points.length-1]!.at),points.length.toLocaleString()+' 測位点'])).addTo(routeLayer)
 }
 if(overlay.value&&result.value){
  const data=result.value
  if(mode.value==='grid')for(const cell of data.cells)L.rectangle(cell.bounds,{className:'radio-overlay',color:cell.color,fillOpacity:cell.low_confidence?.25:.5,weight:1,dashArray:cell.low_confidence?'3 3':undefined}).bindPopup(popup(['中央値 '+cell.median.toFixed(1)+' '+data.unit,cell.sample_count+' 観測 / '+(data.tripCountKnown?cell.distinct_trip_count+' 走行':'走行数不明')])).addTo(radioLayer)
  for(const point of overlayPoints.value){
   const dot=L.circleMarker([point.gps.lat,point.gps.lon],{className:'radio-overlay',radius:5,color:point.color,fillColor:point.color,fillOpacity:point.state==='missing'?.2:.9,weight:point.accuracyUnknown?1:2,dashArray:point.accuracyUnknown?'2 2':undefined})
   dot.bindPopup(popup([fmt(point.at),metric.value.toUpperCase()+' '+(point.value===null?'未取得':point.value+' '+data.unit),(point.carrier||'回線不明')+' / '+(point.band||'バンド不明'),point.accuracyUnknown?'位置精度: 不明':'位置精度: '+point.gps.accuracyM+' m']))
   dot.on('click',()=>selected.value=point);dot.addTo(radioLayer)
  }
 }
 routeLayer.invoke('bringToFront')
 if(!fitted)fit()
 renderBackground()
}
function renderBackground(){if(!map)return;if(online.value&&!empty.value&&!tiles){tileFailed.value=false;tiles=mapTiles(()=>tileFailed.value=true).addTo(map)}else if(!online.value||empty.value){tiles?.remove();tiles=undefined;tileFailed.value=false}}
function refresh(){void loadTrack();void loadRadio()}
function download(format:'csv'|'geojson'){
 const data=result.value;if(!data?.points.length)return
 let body='',type=''
 if(format==='geojson'){body=JSON.stringify({type:'FeatureCollection',features:data.points.map(p=>({type:'Feature',geometry:{type:'Point',coordinates:[p.gps.lon,p.gps.lat]},properties:{at:p.at,metric:metric.value,value:p.value,state:p.state,carrier:p.carrier,band:p.band,accuracyM:p.gps.accuracyM??null,source:p.source}}))},null,2);type='application/geo+json'}
 else{const escape=(v:unknown)=>'"'+String(v??'').replaceAll('"','""')+'"';body='\ufeff'+[['日時','緯度','経度','指標','値','状態','回線','バンド','精度m','取得元'],...data.points.map(p=>[p.at,p.gps.lat,p.gps.lon,metric.value,p.value,p.state,p.carrier,p.band,p.gps.accuracyM,p.source])].map(row=>row.map(escape).join(',')).join('\r\n');type='text/csv;charset=utf-8'}
 const url=URL.createObjectURL(new Blob([body],{type})),anchor=document.createElement('a');anchor.href=url;anchor.download='np1-radio-'+day.value+'.'+format;anchor.click();URL.revokeObjectURL(url)
}
watch(day,()=>{fitted=false;void loadTrack();void loadRadio()})
watch([overlay,metric,carrier,band,size,unknown],loadRadio);watch(mode,render);watch(online,renderBackground)
function visibility(){if(!document.hidden)refresh()}
onMounted(()=>{if(!container.value)return;map=L.map(container.value).setView([36.2,138.2],5);map.attributionControl.setPrefix(false);radioLayer=L.layerGroup().addTo(map);routeLayer=L.layerGroup().addTo(map);resize=new ResizeObserver(()=>map?.invalidateSize({pan:false}));resize.observe(container.value);refresh();timer=setInterval(()=>{if(!document.hidden)refresh()},30000);document.addEventListener('visibilitychange',visibility)})
onUnmounted(()=>{active=false;sequence++;radioSequence++;request?.abort();radioRequest?.abort();clearInterval(timer);document.removeEventListener('visibilitychange',visibility);resize?.disconnect();map?.remove()})
</script>
<template>
 <div class="radio-survey journey-map">
  <div class="radio-toolbar journey-map-toolbar"><label class="radio-check"><input v-model="overlay" type="checkbox">電波強度のヒートマップを表示</label><label class="radio-check"><input v-model="online" type="checkbox">背景地図を表示</label><button class="button secondary radio-refresh" :disabled="loading||radioLoading" @click="refresh">{{loading?'取得中…':'更新'}}</button></div>
  <div v-if="error" class="radio-error" role="alert">{{error}}<button class="button secondary" @click="loadTrack">再試行</button></div>
  <div class="radio-map-frame"><div ref="container" class="radio-map-canvas" aria-label="GPSの走行履歴地図"></div><div v-if="empty&&!error" class="radio-empty"><strong>{{loading?'走行経路を読み込み中…':'この日の位置記録はありません'}}</strong><p>{{track?.reason||'録画に保存されたGPS測位から走行経路を表示します。'}}</p></div><button v-if="!empty" class="radio-fit button secondary" @click="fit">全体を表示</button></div>
  <div class="radio-observation-status journey-map-status" aria-live="polite"><span><strong>{{track?.points.length.toLocaleString()??'—'}}</strong> GPS測位点</span><span><strong>{{track?.segments.length.toLocaleString()??'—'}}</strong> 走行区間</span><span>{{day}}（日本時間）</span></div>
  <p v-if="track?.sourceTruncated" class="radio-note">一部の位置記録は読み取り範囲に含まれていません。</p>
  <p v-if="track?.source==='ncc-retained-measured-positions'" class="radio-note">保存済みのGPS測位を表示しています。経路が一部しか残っていなくても、距離・時間の記録は日別の詳細で確認できます。</p>
  <p v-if="tileFailed" class="radio-note" role="note">背景地図を読み込めませんでした。走行経路は引き続き表示します。</p>
  <p class="radio-note">青い線はGPSの走行経路です。測位が途切れた区間はつなぎません。</p>
  <template v-if="overlay">
   <details open class="radio-filters"><summary>電波の絞り込み・表示設定</summary><div class="radio-filter-content"><label>電波の指標<select v-model="metric"><option value="rsrp">受信強度 · RSRP</option><option value="sinr">信号品質 · SINR</option><option value="rsrq">受信品質 · RSRQ</option></select></label><label>表示<select v-model="mode"><option value="grid">エリア集計</option><option value="points">測定点</option></select></label><label v-if="mode==='grid'">区画サイズ<select v-model="size"><option value="20">20 m</option><option value="50">50 m</option><option value="100">100 m</option></select></label><label>回線<select v-model="carrier"><option value="">すべての回線</option><option v-for="name in result?.carriers" :key="name">{{name}}</option></select></label><label>バンド<select v-model="band"><option value="">すべてのバンド</option><option v-for="name in result?.bands" :key="name">{{name}}</option></select></label><label class="radio-check"><input v-model="unknown" type="checkbox">精度不明の測定点も表示</label></div><p>GPS経路は電波の絞り込みに関係なく表示します。電波の位置は時刻差5秒以内で照合し、精度50 m超・HDOP 5超は除外します。精度不明の点はエリア集計に含めません。</p></details>
   <div v-if="radioError" class="radio-error" role="status">{{radioError}}<button class="button secondary" @click="loadRadio">電波履歴を再試行</button></div>
   <div class="radio-observation-status"><span><strong>{{result?.total?.toLocaleString()??'—'}}</strong> 電波観測</span><span><strong>{{result?.points.length.toLocaleString()??'—'}}</strong> 位置付き</span><span v-if="result?.unplaced">対応する測位なし {{result.unplaced.toLocaleString()}}</span><span v-if="result?.accuracyExcluded">精度不明 {{result.accuracyExcluded.toLocaleString()}}</span><span v-if="result?.truncated">直近20,000件を表示</span></div>
   <p v-if="!radioLoading&&!radioError&&!overlayPoints.length&&!(mode==='grid'&&result?.cells.length)" class="radio-note" role="status">この日の表示条件に合う電波観測はありません。GPSの走行経路は独立して表示します。</p>
   <p v-if="mode==='grid'&&overlayPoints.some(point=>point.accuracyUnknown)" class="radio-note" role="note">メートル単位の精度が記録されていない位置は、破線の測定点で表示します。エリア集計には含めません。</p>
   <div class="radio-legend"><strong>{{metric.toUpperCase()}} / {{legend?.unit}}</strong><span v-for="item in legend?.thresholds" :key="item.label"><i :style="{background:item.color}"></i>{{item.minimum===null?'下限未満':`≥ ${item.minimum}`}} {{item.label}}</span><span><i style="background:#3f4a5a"></i>圏外</span><span><i style="background:#a8afb8"></i>未取得</span></div>
   <p class="radio-note">{{mode==='grid'?(result?.tripWeighted?'走行ごとの中央値をエリア集計。':'全観測の中央値をエリア集計。')+'観測3件未満は薄く表示します。':''}} {{legend?.note}}</p>
   <div v-if="selected" class="radio-selected"><strong>{{fmt(selected.at)}}</strong><span>{{metric.toUpperCase()}} {{selected.value??'未取得'}} {{selected.value===null?'':result?.unit}}</span><span>{{selected.carrier||'回線不明'}} / {{selected.band||'バンド不明'}}</span></div>
   <div class="radio-export"><span>表示対象の電波測定点を書き出す</span><button class="button secondary" :disabled="!result?.points.length" @click="download('csv')">CSV</button><button class="button secondary" :disabled="!result?.points.length" @click="download('geojson')">GeoJSON</button></div>
  </template>
 </div>
</template>
