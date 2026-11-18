<script setup lang="ts">
import {computed,onMounted,onUnmounted,ref,watch} from 'vue'
import {Camera,Maximize} from '@lucide/vue'
import {api} from '../services/api'

const props=defineProps<{source:'front'|'in';format:'h264'|'jpeg';color:string;jpegResolution:'360p'|'1080p';playing:boolean}>(),emit=defineEmits<{fallback:[];busy:[value:boolean]}>()
const source=computed(()=>props.source),format=computed(()=>props.format),color=computed(()=>props.color),jpegResolution=computed(()=>props.jpegResolution),active=ref(false),busy=ref(false),status=ref('idle'),error=ref(''),imageURL=ref(''),video=ref<HTMLVideoElement>(),width=ref(1920),height=ref(1080),fps=ref(0),hasFrame=ref(false)
let generation=0,session='',controller:AbortController|undefined,timer:ReturnType<typeof setTimeout>|undefined,waitingTimer:ReturnType<typeof setTimeout>|undefined,finishPause:(()=>void)|undefined,peer:RTCPeerConnection|undefined,objectURL=''
const caption=computed(()=>format.value==='h264'?`${width.value} × ${height.value}${fps.value?' · '+Number(fps.value.toFixed(1))+' fps':''} · H.264`:`${jpegResolution.value==='1080p'?'1920 × 1080':'640 × 360'} · 最大 1 fps · JPEG`)
const waiting=computed(()=>active.value&&!hasFrame.value&&status.value!=='live')
function clearMedia(){imageURL.value='';if(objectURL)URL.revokeObjectURL(objectURL);objectURL='';if(video.value)video.value.srcObject=null;peer?.close();peer=undefined}
async function release(id:string,keepalive=false){if(id){try{await api('/camera/sessions/'+encodeURIComponent(id),'DELETE',undefined,true,undefined,keepalive)}catch{}}}
async function stop(reset=true,keepalive=false){active.value=false;generation++;busy.value=false;controller?.abort();clearTimeout(timer);clearTimeout(waitingTimer);finishPause?.();finishPause=undefined;const id=session;session='';clearMedia();hasFrame.value=false;if(reset){status.value='idle';error.value=''}await release(id,keepalive)}
function pause(ms:number,g:number){return new Promise<void>(resolve=>{if(!active.value||g!==generation)return resolve();finishPause=resolve;timer=setTimeout(()=>{finishPause=undefined;resolve()},ms)})}
async function createSession(g:number){const value=await api<{id:string}>('/camera/sessions','POST',{source:source.value,color:format.value==='jpeg'?color.value:'source',resolution:format.value==='jpeg'?jpegResolution.value:'360p'});if(g!==generation||!active.value){await release(value.id);return false}session=value.id;return true}
async function start(){
 if(active.value||busy.value)return
 error.value=''
 if(format.value==='h264'&&typeof RTCPeerConnection==='undefined'){status.value='unsupported';error.value='このブラウザーはWebRTCライブ表示に対応していません。JPEG表示を利用できます。';return}
 busy.value=true;active.value=true;hasFrame.value=false;status.value='loading';const g=++generation
 try{if(await createSession(g)){busy.value=false;if(format.value==='h264')await h264Loop(g);else await jpegLoop(g)}}catch{if(g===generation){await stop(false);status.value='error';error.value='映像を開けません。もう一度お試しください。'}}finally{if(g===generation)busy.value=false}
}
async function change(){const wasActive=active.value||props.playing;await stop();if(wasActive)await start()}
async function jpegLoop(g:number){
 while(active.value&&g===generation){
  controller=new AbortController()
  try{
   const response=await fetch('/api/v1/camera/sessions/'+encodeURIComponent(session)+'/stream',{signal:controller.signal})
   if(g!==generation)return
   if(response.status===403){await release(session);if(!await createSession(g))return;continue}
   if(!response.ok){status.value='waiting';imageURL.value='';await pause(2000,g);continue}
   const blob=await response.blob();if(g!==generation)return
   if(objectURL)URL.revokeObjectURL(objectURL);objectURL=URL.createObjectURL(blob);imageURL.value=objectURL;status.value='live';error.value=''
   await pause(1000,g)
  }catch(e){if(g!==generation||(e instanceof DOMException&&e.name==='AbortError'))return;status.value='waiting';imageURL.value='';await pause(2000,g)}
 }
}
function iceGathered(connection:RTCPeerConnection,signal:AbortSignal){return new Promise<void>((resolve,reject)=>{
 if(connection.iceGatheringState==='complete')return resolve()
 const cleanup=()=>{connection.removeEventListener('icegatheringstatechange',changed);signal.removeEventListener('abort',aborted);clearTimeout(timeout)}
 const changed=()=>{if(connection.iceGatheringState==='complete'){cleanup();resolve()}},aborted=()=>{cleanup();reject(new DOMException('Aborted','AbortError'))},timeout=setTimeout(()=>{cleanup();reject(new Error('ICE gathering timeout'))},5000)
 connection.addEventListener('icegatheringstatechange',changed);signal.addEventListener('abort',aborted,{once:true})
})}
async function h264Loop(g:number){
 while(active.value&&g===generation){
  const abort=new AbortController();controller=abort
  try{
   status.value='loading';clearMedia()
   const connection=new RTCPeerConnection({iceServers:[]});peer=connection
   connection.addTransceiver('video',{direction:'recvonly'})
   let finish!:()=>void,disconnectTimer:ReturnType<typeof setTimeout>|undefined
   const ended=new Promise<void>(resolve=>{finish=resolve})
   connection.ontrack=event=>{if(g!==generation)return;const stream=event.streams[0]||new MediaStream([event.track]);if(video.value){video.value.srcObject=stream;void video.value.play().catch(()=>{})}}
   connection.onconnectionstatechange=()=>{clearTimeout(disconnectTimer);if(connection.connectionState==='failed'||connection.connectionState==='closed')finish();else if(connection.connectionState==='disconnected')disconnectTimer=setTimeout(()=>{if(connection.connectionState==='disconnected')finish()},2000)}
   await connection.setLocalDescription(await connection.createOffer())
   await iceGathered(connection,abort.signal)
   if(!connection.localDescription)throw new Error('WebRTC offer unavailable')
   const answer=await api<{type:'answer';sdp:string;width:number;height:number;fps:number;transport:string}>('/camera/sessions/'+encodeURIComponent(session)+'/webrtc','POST',{type:connection.localDescription.type,sdp:connection.localDescription.sdp},true,abort.signal)
   if(answer.transport!=='webrtc-udp-h264'||!answer.sdp)throw new Error('Unexpected WebRTC answer')
   width.value=answer.width||1920;height.value=answer.height||1080;fps.value=answer.fps||0
   await connection.setRemoteDescription({type:answer.type,sdp:answer.sdp})
   await ended;clearTimeout(disconnectTimer)
   }catch(e){if(g!==generation||(e instanceof DOMException&&e.name==='AbortError'))return}
   finally{if(peer){peer.close();peer=undefined}abort.abort()}
  if(active.value&&g===generation){status.value='waiting';await pause(500,g)}
 }
}
function fallback(){emit('fallback')}
function fullscreen(){video.value?.requestFullscreen?.().catch(()=>{})}
function videoWaiting(){clearTimeout(waitingTimer);waitingTimer=setTimeout(()=>{if(active.value&&!hasFrame.value&&video.value?.readyState!==4)status.value='waiting'},700)}
function videoPlaying(){clearTimeout(waitingTimer);if(active.value){if(video.value?.videoWidth)width.value=video.value.videoWidth;if(video.value?.videoHeight)height.value=video.value.videoHeight;hasFrame.value=true;status.value='live';error.value=''}}
watch(()=>props.playing,value=>{if(value)start();else stop()},{immediate:true})
watch([format,color,jpegResolution],()=>{change()})
watch(busy,value=>emit('busy',value),{immediate:true})
function pageHide(){void stop(false,true)}
onMounted(()=>window.addEventListener('pagehide',pageHide))
onUnmounted(()=>{window.removeEventListener('pagehide',pageHide);void stop()})
defineExpose({stop})
</script>

<template>
 <section class="panel camera-panel" :aria-busy="busy">
  <div class="panel-head"><h2>{{source==='front'?'前方カメラ':'車内カメラ'}}</h2><span class="tag">{{caption}}</span><button v-if="format==='h264'&&hasFrame" class="icon-button" :aria-label="(source==='front'?'前方':'車内')+'映像を全画面表示'" @click="fullscreen"><Maximize :size="18"/></button></div>
  <div class="camera-preview live-camera">
   <video v-if="format==='h264'" ref="video" aria-label="純正H.264 WebRTCライブ映像" muted playsinline autoplay @waiting="videoWaiting" @playing="videoPlaying"></video>
   <img v-else-if="imageURL" :src="imageURL" alt="ライブカメラ映像">
   <div v-if="(!active||waiting)&&!imageURL" class="camera-placeholder"><Camera :size="40" stroke-width="1"/><h3>{{waiting?'映像の再開待ち':'カメラは停止しています'}}</h3><p>{{waiting?'カメラ出力が再開すると自動で表示します。':'表示ボタンでライブ映像を開きます。'}}</p></div>
  </div>
  <p v-if="waiting" class="hint pad" role="status">カメラ出力の再開を待っています。</p>
  <p v-if="error" class="error pad" role="alert">{{error}} <button v-if="status==='unsupported'" class="text-button" @click="fallback">JPEG表示に切り替える</button></p>
 </section>
</template>

<style scoped>
.live-camera{position:relative;aspect-ratio:16/9;min-height:180px;background:#090b0e;overflow:hidden}.live-camera video,.live-camera img{display:block;width:100%;height:100%;object-fit:contain}.camera-placeholder{position:absolute;inset:0;display:flex;align-items:center;justify-content:center;flex-direction:column;padding:20px;text-align:center;color:#b6c0cf;background:#11151c}.camera-placeholder h3{font-size:18px;margin:16px 0 8px}.camera-placeholder p{font-size:14px;margin:0;line-height:1.7}.camera-controls{flex-wrap:wrap}.camera-controls select,.camera-controls button{min-height:44px}.pad{margin:0;padding:0 20px 18px}@media(max-width:600px){.camera-controls select{flex:1;min-width:120px}.camera-controls .primary,.camera-controls .secondary{flex:1}.live-camera{min-height:170px}.camera-placeholder{padding:14px}.camera-placeholder h3{font-size:16px}.camera-placeholder p{font-size:13px}}
</style>
