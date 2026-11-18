import {test,expect} from '@playwright/test'
import {writeFile} from 'node:fs/promises'
test.skip(process.env.NCC_H264_HARDWARE!=='1','Explicit live-camera field flag required')

test('both original H264 camera streams decode over WebRTC and survive shared view switching',async({page},info)=>{
 const soakSeconds=Number(process.env.NCC_H264_SECONDS||0)
 if(!Number.isInteger(soakSeconds)||soakSeconds<0||soakSeconds>240)throw new Error('NCC_H264_SECONDS must be 0..240')
 test.setTimeout(90000+soakSeconds*1000)
 const journal:unknown[]=[],note=(value:unknown)=>{if(journal.length<200)journal.push({at:Date.now(),value})}
 page.on('console',message=>{if(message.text().startsWith('NCC_MEDIA '))note(message.text().slice(10))})
 page.on('requestfailed',request=>{if(request.url().endsWith('/webrtc'))note({requestFailed:request.failure()?.errorText})})
 const errors:string[]=[];page.on('pageerror',error=>errors.push(error.message))
 let responses=0;page.on('response',response=>{if(/\/camera\/sessions\/[^/]+\/webrtc$/.test(new URL(response.url()).pathname)){note({response:response.status()});if(response.status()===200)responses++}})
 try{
 await page.goto('/#camera')
 await page.getByRole('button',{name:'並べて表示',exact:true}).click()
 await page.getByRole('button',{name:'映像を表示',exact:true}).click()
 const videos=page.locator('.live-camera-grid video')
 await expect(videos).toHaveCount(2)
 // DriveRec has more than one stock front encoder and may expose either its
 // 1080p ordinary output or 720p companion output first. Both are original
 // H.264 encoder taps sent as RTP without MP4/MSE remuxing.
 await expect.poll(()=>videos.evaluateAll(items=>items.every(item=>{const video=item as HTMLVideoElement;return video.readyState>=2&&[[1920,1080],[1280,720]].some(([width,height])=>video.videoWidth===width&&video.videoHeight===height)})),{timeout:25000}).toBe(true)
 await page.waitForTimeout(2000)
 const startup=await videos.evaluateAll(items=>items.map(item=>{const video=item as HTMLVideoElement;return {ready:video.readyState,width:video.videoWidth,height:video.videoHeight,frames:video.getVideoPlaybackQuality().totalVideoFrames}}))
 note({startup})
 const frames=await videos.evaluateAll(items=>items.map(item=>(item as HTMLVideoElement).getVideoPlaybackQuality().totalVideoFrames))
 await expect.poll(()=>videos.evaluateAll((items,before)=>items.every((item,index)=>(item as HTMLVideoElement).getVideoPlaybackQuality().totalVideoFrames>before[index]+8),frames),{timeout:35000}).toBe(true)
 const next=await videos.evaluateAll(items=>items.map(item=>(item as HTMLVideoElement).getVideoPlaybackQuality().totalVideoFrames))
 note({initialFrames:frames,advancedFrames:next})
 expect(next.every((value,index)=>value>frames[index]+8)).toBe(true)
 if(soakSeconds){
  const connections=responses,started=Date.now()
  while(Date.now()-started<soakSeconds*1000){
   await page.waitForTimeout(Math.max(1,Math.min(1000,soakSeconds*1000-(Date.now()-started))))
   const playback=await videos.evaluateAll(items=>items.map(item=>{const video=item as HTMLVideoElement;return {ready:video.readyState,paused:video.paused,frames:video.getVideoPlaybackQuality().totalVideoFrames}}))
   note({playback})
   expect(await videos.evaluateAll(items=>items.every(item=>!(item as HTMLVideoElement).error))).toBe(true)
  }
  await expect.poll(()=>videos.evaluateAll(items=>items.every(item=>(item as HTMLVideoElement).readyState>=2)),{timeout:20000}).toBe(true)
  const final=await videos.evaluateAll(items=>items.map(item=>(item as HTMLVideoElement).getVideoPlaybackQuality().totalVideoFrames))
  note({finalFrames:final,connections,responses})
  expect(final.every((value,index)=>value>next[index]+soakSeconds*8)).toBe(true)
  expect(responses).toBe(connections)
 }
 await page.screenshot({path:info.outputPath('h264-live-dual-desktop.png'),fullPage:true})
 await page.getByRole('button',{name:'車内',exact:true}).click()
 await expect(videos).toHaveCount(1)
 await expect(page.getByRole('heading',{name:'車内カメラ',exact:true})).toBeVisible()
 await page.getByRole('button',{name:'並べて表示',exact:true}).click()
 await expect(videos).toHaveCount(2)
 await expect.poll(()=>videos.evaluateAll(items=>items.every(item=>(item as HTMLVideoElement).readyState>=2)),{timeout:20000}).toBe(true)
 await page.setViewportSize({width:390,height:844})
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true)
 await page.screenshot({path:info.outputPath('h264-live-dual-mobile.png'),fullPage:true})
 await page.getByRole('button',{name:'表示を停止',exact:true}).click()
 await expect.poll(()=>videos.evaluateAll(items=>items.every(item=>!(item as HTMLVideoElement).getAttribute('src')))).toBe(true)
 expect(errors).toEqual([])
 expect(responses).toBeGreaterThanOrEqual(2)
 }finally{await writeFile(info.outputPath('media-diagnostics.private.json'),JSON.stringify(journal,null,2))}
})

test('WebRTC remains continuous after delayed HTTP signaling',async({page},info)=>{
 test.skip(process.env.NCC_H264_LOW_BANDWIDTH!=='1','Explicit low-bandwidth field flag required')
 test.setTimeout(70000)
 let responses=0
 page.on('response',response=>{if(/\/camera\/sessions\/[^/]+\/webrtc$/.test(new URL(response.url()).pathname)&&response.status()===200)responses++})
 await page.goto('/#camera')
 // Signaling is the only WebRTC traffic carried over HTTP. Delay that one
 // response without throttling the UDP/RTP media path itself: CDP's global
 // network emulation also constrains UDP and therefore does not represent a
 // slow signaling connection.
 await page.route('**/api/v1/camera/sessions/*/webrtc',async route=>{
  const response=await route.fetch()
  await new Promise(resolve=>setTimeout(resolve,1500))
  await route.fulfill({response})
 })
 await page.getByRole('button',{name:'映像を表示',exact:true}).click()
 const video=page.locator('.live-camera video')
 await expect.poll(()=>video.evaluate(item=>{const media=item as HTMLVideoElement;return media.readyState>=2&&media.getVideoPlaybackQuality().totalVideoFrames>4}),{timeout:30000}).toBe(true)
 const samples:{ready:number;frames:number}[]=[]
 for(let second=0;second<20;second++){
  await page.waitForTimeout(1000)
  expect(await page.getByText('カメラ出力の再開を待っています。',{exact:true}).count()).toBe(0)
  samples.push(await video.evaluate(item=>{const media=item as HTMLVideoElement;return {ready:media.readyState,frames:media.getVideoPlaybackQuality().totalVideoFrames}}))
 }
 await writeFile(info.outputPath('low-bandwidth-samples.private.json'),JSON.stringify({responses,samples},null,2))
 expect(samples.filter(item=>item.ready>=2).length).toBeGreaterThanOrEqual(18)
 expect(samples.at(-1)!.frames-samples[0]!.frames).toBeGreaterThan(120)
 expect(responses).toBe(1)
 await page.screenshot({path:info.outputPath('h264-live-low-bandwidth.png'),fullPage:true})
})
