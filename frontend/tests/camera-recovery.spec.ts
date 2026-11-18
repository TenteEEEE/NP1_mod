import {test,expect,type Page} from '@playwright/test'
const url=process.env.NCC_UI_REVIEW_URL
const png=Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=','base64')

async function open(page:Page,stream:(id:string)=>Promise<{status:number;body?:Buffer}>,format:'jpeg'|'h264'='jpeg'){
 let created=0,deleted=0;const resolutions:string[]=[]
 await page.route('**/api/v1/**',async route=>{
  const path=new URL(route.request().url()).pathname,method=route.request().method()
  if(path.endsWith('/events'))return route.fulfill({contentType:'text/event-stream',body:': fixture\n\n'})
  if(path.endsWith('/camera/sessions')&&method==='POST'){created++;resolutions.push(route.request().postDataJSON().resolution);return route.fulfill({status:201,json:{id:'fixture-'+created}})}
  if(path.includes('/camera/sessions/')&&path.endsWith('/webrtc'))return route.fulfill({json:{type:'answer',sdp:'synthetic-answer',width:1920,height:1080,fps:27,transport:'webrtc-udp-h264'}})
  if(path.includes('/camera/sessions/')&&path.endsWith('/stream')){const value=await stream(path.split('/').at(-2)!);return route.fulfill({status:value.status,contentType:value.status===200?'image/png':'text/plain',body:value.body||'unavailable'})}
  if(path.includes('/camera/sessions/')&&method==='DELETE'){deleted++;return route.fulfill({json:{ok:true}})}
  const value=path.endsWith('/auth/session')?{csrf:'fixture-token'}:path.endsWith('/settings')?{revision:1,theme:'dark',locale:'ja',favorites:[]}:path.endsWith('/capabilities')?[]:{}
  await route.fulfill({json:value})
 })
 await page.goto(url!+'#camera')
 if(format==='jpeg')await page.getByRole('combobox',{name:'映像形式'}).selectOption('jpeg')
 await page.getByRole('button',{name:'映像を表示',exact:true}).click()
 return {created:()=>created,deleted:()=>deleted,resolutions:()=>resolutions}
}

test.describe('camera recovery without service actions (synthetic)',()=>{
 test.skip(!url,'Set NCC_UI_REVIEW_URL to isolated Vite')
 test('WebRTC uses one peer without MP4 fragments and keeps the last frame during a short stall',async({page})=>{
  await page.addInitScript(()=>{
   const stats={offers:0,peers:0,plays:0,closed:0};(window as any).mediaFixture=stats
   class PeerFixture extends EventTarget{iceGatheringState='complete';connectionState='new';localDescription:any;ontrack:any;onconnectionstatechange:any;constructor(){super();stats.peers++}addTransceiver(){}async createOffer(){stats.offers++;return {type:'offer',sdp:'synthetic-offer'}}async setLocalDescription(value:any){this.localDescription=value}async setRemoteDescription(){this.connectionState='connected';this.onconnectionstatechange?.();this.ontrack?.({streams:[new MediaStream()],track:{}})}close(){if(this.connectionState==='closed')return;this.connectionState='closed';stats.closed++;this.onconnectionstatechange?.()}}
   ;(window as any).RTCPeerConnection=PeerFixture
   HTMLMediaElement.prototype.play=function(){stats.plays++;return new Promise<void>(()=>{})}
  })
  await open(page,async()=>({status:200,body:png}),'h264')
  await expect.poll(()=>page.evaluate(()=>(window as any).mediaFixture.plays)).toBeGreaterThan(0)
  expect(await page.evaluate(()=>(window as any).mediaFixture.offers)).toBe(1)
  expect(await page.evaluate(()=>(window as any).mediaFixture.peers)).toBe(1)
  const video=page.locator('.live-camera video')
  await video.dispatchEvent('playing')
  await video.dispatchEvent('waiting')
  await page.waitForTimeout(900)
  await expect(page.getByText('カメラ出力の再開を待っています。',{exact:true})).toHaveCount(0)
  await page.getByRole('button',{name:'表示を停止',exact:true}).click()
  await expect.poll(()=>page.evaluate(()=>(window as any).mediaFixture.closed)).toBe(1)
 })
 test('waits for a restarted producer and resumes in the same session',async({page})=>{
  let requests=0
  const state=await open(page,async()=>++requests<3?{status:503}:{status:200,body:png})
  await expect(page.getByText('カメラ出力の再開を待っています。',{exact:true})).toBeVisible()
  await expect(page.getByRole('img',{name:'ライブカメラ映像'})).toBeVisible({timeout:10000})
  await expect(page.getByText('カメラ出力の再開を待っています。',{exact:true})).toHaveCount(0)
  expect(state.created()).toBe(1);expect(state.deleted()).toBe(0)
 })
 test('stopping during producer wait cancels retries and releases the session',async({page})=>{
  let requests=0
  const state=await open(page,async()=>{requests++;return {status:503}})
  await expect(page.getByText('カメラ出力の再開を待っています。',{exact:true})).toBeVisible()
  await page.getByRole('button',{name:'表示を停止',exact:true}).click()
  const before=requests
  await page.waitForTimeout(2200)
  expect(requests).toBe(before);expect(state.deleted()).toBe(1)
 })
 test('JPEG can switch from the lightweight preview to 1920x1080',async({page})=>{
  const state=await open(page,async()=>({status:200,body:png}))
  await expect(page.getByRole('img',{name:'ライブカメラ映像'})).toBeVisible()
  await page.getByRole('button',{name:'表示を停止',exact:true}).click()
  await page.getByRole('combobox',{name:'JPEG解像度'}).selectOption('1080p')
  await page.getByRole('button',{name:'映像を表示',exact:true}).click()
  await expect(page.getByText('1920 × 1080 · 最大 1 fps · JPEG',{exact:true})).toBeVisible()
  await expect.poll(()=>state.resolutions().at(-1)).toBe('1080p')
 })
 for(const width of [320,390,1440])test(`${width}px dual view shares the playback switch and preserves the surviving stream`,async({page})=>{
  await page.setViewportSize({width,height:900})
  const state=await open(page,async()=>({status:200,body:png}))
  await expect(page.getByRole('img',{name:'ライブカメラ映像'})).toHaveCount(1)
  await page.getByRole('button',{name:'並べて表示',exact:true}).click()
  await expect(page.getByRole('img',{name:'ライブカメラ映像'})).toHaveCount(2)
  expect(state.created()).toBe(2)
  await expect(page.getByRole('button',{name:'並べて表示',exact:true})).toHaveAttribute('aria-pressed','true')
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true)
  await page.getByRole('button',{name:'車内',exact:true}).click()
  await expect(page.getByRole('img',{name:'ライブカメラ映像'})).toHaveCount(1)
  await expect(page.getByRole('heading',{name:'車内カメラ',exact:true})).toBeVisible()
  expect(state.created()).toBe(2)
  await expect.poll(()=>state.deleted()).toBe(1)
  await page.getByRole('button',{name:'表示を停止',exact:true}).click()
  await expect.poll(()=>state.deleted()).toBe(2)
 })
})
