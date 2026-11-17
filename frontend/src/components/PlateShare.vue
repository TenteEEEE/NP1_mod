<script setup lang="ts">
import {computed,nextTick,onMounted,onUnmounted,ref,watch} from 'vue'
import {AlertTriangle,CheckCircle2,Cloud,MapPin,RefreshCw,RotateCcw,Search,Upload} from '@lucide/vue'
import {api} from '../services/api'
import {VectorMap,collection,feature,type LatLon} from '../services/vector-map'
import {jstDay,plateSerial,type NumberShareSighting,type NumberShareSightings,type NumberShareStatus,type NumberShareUpload,type NumberShareVehicle,type NumberShareVehicles} from '../services/plate'

const status=ref<NumberShareStatus>(),items=ref<NumberShareSighting[]>([]),vehicles=ref<NumberShareVehicle[]>([]),categories=ref<string[]>([])
const endpoint=ref(''),enabled=ref(false),key=ref(''),showKey=ref(false),clearKey=ref(false),query=ref(''),scope=ref<'all'|'mine'|'others'>('all'),matchStatus=ref('')
const vehicleQuery=ref(''),serial=ref(''),category=ref('')
const uploadFrom=ref(jstDay(-6)),uploadTo=ref(jstDay(0)),uploading=ref(false)
const loading=ref(false),saving=ref(false),checking=ref(false),error=ref(''),notice=ref(''),syncedAt=ref(''),online=ref(true)
const mapEl=ref<HTMLElement>(),selected=ref('')
let map:VectorMap|undefined,statusTimer:ReturnType<typeof setInterval>|undefined
const message=(e:unknown)=>e instanceof Error?e.message:String(e)
const time=(v?:string)=>v?new Date(v).toLocaleString('ja-JP',{timeZone:'Asia/Tokyo',month:'numeric',day:'numeric',hour:'2-digit',minute:'2-digit'}):'—'
const display=(p:NumberShareSighting['plate']|NumberShareVehicle)=>[p.region,p.classification_number,p.hiragana,plateSerial(p.serial_number)].filter(Boolean).join(' ')
const located=computed(()=>items.value.filter(item=>item.location))

async function loadStatus(){
 try{const first=!status.value;status.value=await api<NumberShareStatus>('/plate/share/status');if(first){endpoint.value=status.value.config.endpoint;enabled.value=status.value.config.enabled;key.value=status.value.config.apiKey||''}}catch(e){error.value=message(e)}
}
async function saveConfig(){
 if(!status.value)return;saving.value=true;error.value=''
 try{const body:{expectedRevision:number;enabled:boolean;endpoint:string;key?:string}={expectedRevision:status.value.config.revision,enabled:enabled.value,endpoint:endpoint.value};if(clearKey.value)body.key='';else if(key.value)body.key=key.value
  await api('/plate/share/config','PUT',body);key.value='';clearKey.value=false;notice.value='NumberShare API設定を保存しました';status.value=undefined;await loadStatus()
 }catch(e){error.value=message(e)}finally{saving.value=false}
}
async function check(){checking.value=true;error.value='';try{const r=await api<{identity?:{user_handle?:string}}>('/plate/share/check','POST');notice.value=r.identity?.user_handle?`接続しました（匿名ID: ${r.identity.user_handle}）`:'APIへ接続しました'}catch(e){error.value=message(e)}finally{checking.value=false}}
async function retry(id:string){try{await api('/plate/share/retry/'+encodeURIComponent(id),'POST');notice.value='再送待ちへ戻しました';await loadStatus()}catch(e){error.value=message(e)}}
async function uploadRange(){
 uploading.value=true;error.value=''
 try{const result=await api<NumberShareUpload>('/plate/share/upload','POST',{from:uploadFrom.value,to:uploadTo.value})
  notice.value=result.queued||result.requeued?`${result.queued}件を追加、${result.requeued}件を再送待ちへ戻しました${result.existing?`（登録済み ${result.existing}件）`:''}`:`対象${result.total}件はすべて送信済みまたは送信待ちです`
  await loadStatus();await loadShared()
 }catch(e){error.value=message(e)}finally{uploading.value=false}
}
async function loadShared(){
 loading.value=true;error.value='';try{const q=new URLSearchParams();if(query.value.trim())q.set('q',query.value.trim());if(scope.value!=='all')q.set('scope',scope.value);if(matchStatus.value)q.set('matchStatus',matchStatus.value)
  const result=await api<NumberShareSightings>('/plate/share/sightings?'+q);items.value=result.items;syncedAt.value=result.syncedAt;selected.value='';render()
 }catch(e){items.value=[];error.value=message(e);render()}finally{loading.value=false}
}
async function searchVehicles(){
 error.value='';try{const q=new URLSearchParams();if(vehicleQuery.value.trim())q.set('q',vehicleQuery.value.trim());if(serial.value.trim())q.set('serialNumber',serial.value.trim());if(category.value)q.set('category',category.value)
  const result=await api<NumberShareVehicles>('/plate/share/vehicles?'+q);vehicles.value=result.items;categories.value=result.categories||[]
 }catch(e){vehicles.value=[];error.value=message(e)}
}
function popup(item:NumberShareSighting){const node=document.createElement('div'),title=document.createElement('strong'),meta=document.createElement('div');title.textContent=display(item.plate);meta.textContent=`${item.mine?'自分の検知':'共有された検知'} · ${time(item.observed_at)}`;node.append(title,meta);return node}
function render(){if(!map)return;const points=located.value.map(item=>feature({type:'Point',coordinates:[item.location!.longitude,item.location!.latitude]},{id:item.event_id,mine:item.mine,selected:item.event_id===selected.value}))
 const layer=(mine:boolean)=>({id:mine?'share-mine':'share-others',type:'circle' as const,source:'number-share',filter:['==',['get','mine'],mine] as any,paint:{'circle-radius':['case',['get','selected'],11,mine?8:6] as any,'circle-color':mine?'#e8590c':'#397fd7','circle-stroke-color':'#fff','circle-stroke-width':['case',['get','selected'],3,1.5] as any,'circle-opacity':.9}})
 map.setOverlay('number-share',collection(points),[layer(false),layer(true)]);map.background(online.value&&points.length>0)
 if(points.length)map.fit(located.value.map(item=>[item.location!.latitude,item.location!.longitude] as LatLon),16)
}
function select(item:NumberShareSighting){selected.value=item.event_id;render();if(item.location&&map){const point:LatLon=[item.location.latitude,item.location.longitude];map.centre(point);map.popup(point,popup(item))}}
function initMap(){if(map||!mapEl.value)return;map=new VectorMap({view:'plates',container:mapEl.value});map.map.on('click',event=>{const hit=map?.features(event,['share-mine','share-others'])[0];const item=items.value.find(value=>value.event_id===String(hit?.properties.id));if(item)select(item)});render()}
watch(online,render)
watch(notice,value=>{if(value)setTimeout(()=>notice.value='',4000)})
onMounted(async()=>{await loadStatus();await nextTick();initMap();if(status.value?.config.keyConfigured){await Promise.all([loadShared(),searchVehicles()])}statusTimer=setInterval(loadStatus,5000)})
onUnmounted(()=>{if(statusTimer)clearInterval(statusTimer);map?.destroy()})
</script>

<template>
 <div class="plate-share">
  <div v-if="error" class="banner error" role="alert"><AlertTriangle :size="16"/><span>{{error}}</span></div>
  <div v-if="notice" class="banner success" role="status"><CheckCircle2 :size="16"/>{{notice}}</div>
  <section class="panel share-config">
   <div class="panel-head"><h2><Cloud :size="18"/>NumberShare API</h2><span v-if="status" class="share-state" :class="status.config.enabled?'on':'off'">{{status.config.enabled?'リアルタイム送信中':'送信停止中'}}</span></div>
   <div class="share-panel-body">
   <div class="share-config-grid">
    <label class="share-endpoint">APIエンドポイント<input v-model.trim="endpoint" type="url" placeholder="https://example.jp/api/" autocomplete="off" spellcheck="false"></label>
    <label>認証キー<input v-model="key" :type="showKey?'text':'password'" placeholder="APIキーを入力" :disabled="clearKey" autocomplete="off" spellcheck="false"></label>
    <label class="plate-check"><input v-model="showKey" type="checkbox">認証キーを表示する</label>
    <label class="plate-check"><input v-model="enabled" type="checkbox">検知をリアルタイム送信する</label>
    <label v-if="status?.config.keyConfigured" class="plate-check"><input v-model="clearKey" type="checkbox">保存済みキーを削除する</label>
   </div>
   <p class="hint">保存済みの認証キーを読み込み、通常は伏せ字で表示します。「認証キーを表示する」を選んだときだけ平文表示します。HTTPS証明書の検証は常に有効です。</p>
   <div class="share-actions"><button class="secondary" :disabled="!status?.config.keyConfigured||checking" @click="check"><RefreshCw :size="14"/>{{checking?'確認中…':'接続を確認'}}</button><button class="primary" :disabled="!status||saving" @click="saveConfig">{{saving?'保存中…':'設定を保存'}}</button></div>
   </div>
  </section>

  <section class="panel share-queue">
   <div class="panel-head"><h2>送信状況</h2><button class="text-button" @click="loadStatus"><RefreshCw :size="14"/>更新</button></div>
   <div class="share-panel-body">
   <dl v-if="status" class="share-counts"><div><dt>送信待ち</dt><dd>{{status.pending}}</dd></div><div><dt>送信済み</dt><dd>{{status.sent}}</dd></div><div><dt>確認が必要</dt><dd :class="{warn:status.blocked}">{{status.blocked}}</dd></div></dl>
   <div v-if="status?.items.length" class="share-queue-list">
    <article v-for="item in status.items" :key="item.eventId"><div><strong>{{display(item.payload.plate)}}</strong><span>{{time(item.payload.observed_at)}} · {{item.state==='sent'?'送信済み':item.state==='blocked'?'自動再送を停止':'再送待ち'}} · {{item.attempts}}回試行</span><small v-if="item.lastError">{{item.lastError}}</small></div><button v-if="item.state==='blocked'" class="secondary compact" @click="retry(item.eventId)"><RotateCcw :size="13"/>再送</button></article>
   </div>
   <p v-else class="plate-empty">送信対象はまだありません。</p>
   </div>
  </section>

  <section class="panel share-upload">
   <div class="panel-head"><h2><Upload :size="18"/>期間を指定してアップロード</h2></div>
   <div class="share-panel-body">
   <div class="plate-toolbar"><label>開始日<input v-model="uploadFrom" type="date"></label><label>終了日<input v-model="uploadTo" type="date"></label><button class="primary" :disabled="uploading||!status?.config.keyConfigured||!uploadFrom||!uploadTo" @click="uploadRange"><Upload :size="14"/>{{uploading?'追加中…':'この期間をアップロード'}}</button></div>
   <p class="hint">端末に保存された期間内の検知をすべて永続キューへ追加します。同じローカル記録は重複追加せず、リアルタイム送信が停止中でもこの手動アップロードは送信されます。</p>
   </div>
  </section>

  <section class="panel share-history">
   <div class="panel-head"><h2>APIの全検知履歴</h2></div>
   <div class="share-panel-body">
   <div class="plate-toolbar">
    <label class="plate-search">ナンバーで検索<input v-model="query" type="search" placeholder="番号・地域・分類・かな"></label>
    <label>表示<select v-model="scope"><option value="all">すべて</option><option value="mine">自分の検知</option><option value="others">他の利用者</option></select></label>
    <label>照合<select v-model="matchStatus"><option value="">すべて</option><option value="exact">確定一致</option><option value="candidate">候補</option><option value="none">一致なし</option></select></label>
    <label class="plate-check"><input v-model="online" type="checkbox">地図の背景を表示</label>
    <button class="secondary compact" :disabled="loading||!status?.config.keyConfigured" @click="loadShared"><RefreshCw :size="14"/>{{loading?'受信中…':'APIから全件受信'}}</button>
   </div>
   <p class="plate-count">{{loading?'APIの全ページを受信中…':`${items.length}件 · 地図に表示 ${located.length}件`}}<span v-if="syncedAt"> · 受信 {{time(syncedAt)}}</span></p>
   <div class="plate-split">
    <div class="plate-map-wrap"><div class="plate-map-frame"><div ref="mapEl" class="plate-map share-map" role="region" aria-label="共有されたナンバーの地図"></div><div v-if="!loading&&!located.length" class="plate-map-placeholder"><MapPin :size="24"/><strong>位置のある共有情報はありません</strong></div></div><div class="plate-legend"><span><i class="dot share-other"></i>他の利用者</span><span><i class="dot share-own"></i>自分</span></div></div>
    <div class="plate-list share-list"><button v-for="item in items" :key="item.event_id" :class="['share-item',{mine:item.mine,selected:item.event_id===selected}]" @click="select(item)"><strong>{{display(item.plate)}}</strong><span><b>{{item.mine?'自分':'他の利用者'}}</b> · {{time(item.observed_at)}} · {{item.match_status==='exact'?'確定一致':item.match_status==='candidate'?'候補':'一致なし'}}</span><small>{{item.location?'位置あり':'位置なし'}}</small></button><p v-if="!loading&&!items.length" class="plate-empty">条件に一致する共有情報はありません。</p></div>
   </div>
   </div>
  </section>

  <section class="panel share-vehicles">
   <div class="panel-head"><h2>登録車両を検索</h2></div>
   <div class="share-panel-body">
   <div class="plate-toolbar"><label class="plate-search">地域・コメント<input v-model="vehicleQuery" type="search" placeholder="部分一致"></label><label>一連指定番号<input v-model="serial" placeholder="12-34"></label><label>カテゴリ<select v-model="category"><option value="">すべて</option><option v-for="value in categories" :key="value">{{value}}</option></select></label><button class="secondary compact" :disabled="!status?.config.keyConfigured" @click="searchVehicles"><Search :size="14"/>検索</button></div>
   <div class="share-vehicle-grid"><article v-for="vehicle in vehicles" :key="vehicle.id"><strong>{{display(vehicle)}}</strong><span>{{vehicle.category}} · 検知 {{vehicle.sightings}}回 · 最終 {{time(vehicle.last_seen)}}</span><p v-if="vehicle.comment">{{vehicle.comment}}</p></article></div>
   <p v-if="!vehicles.length" class="plate-empty">条件に一致する登録車両はありません。</p>
   </div>
  </section>
 </div>
</template>
