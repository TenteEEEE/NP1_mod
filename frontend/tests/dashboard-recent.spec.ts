import {test,expect,type Page} from '@playwright/test'
import {vectorTiles,vectorState,overlayCount,clickVectorFeature} from './fixtures/vector-tiles'
import {rollingHourTicks} from '../src/services/journey'
const now=Date.parse('2026-11-15T00:30:00Z'),since=now-86400000
const observation=(data:unknown)=>({status:'available',data,source:'synthetic-recent-dashboard',verified:false})
async function seed(page:Page){
 await page.clock.install({time:new Date(now)})
 await page.emulateMedia({reducedMotion:'reduce'})
 await page.addInitScript(()=>{localStorage.setItem('ncc.dashboard','overview');localStorage.setItem('ncc.nav','float');(window as any).EventSource=class extends EventTarget{close(){}}})
 const requests:string[]=[]
 await vectorTiles(page)
 await page.route('**/api/v1/**',async route=>{
  const url=new URL(route.request().url()),path=url.pathname;let data:unknown={}
  if(path.endsWith('/auth/session'))data={csrf:'fixture'}
  else if(path.endsWith('/settings'))data={revision:1,theme:'light',locale:'ja',favorites:[]}
  else if(path.endsWith('/system/summary'))data={gps:observation({fix:true,at:new Date(now).toISOString(),lat:35,lon:139,satellites:10}),recording:observation({writing:false})}
  else if(path.endsWith('/device/gps/live'))data=observation({fix:true,at:new Date(now).toISOString(),lat:35,lon:139,satellites:10})
  else if(path.endsWith('/trips')){
   requests.push(url.search)
   const rolling=url.searchParams.get('period')==='24h'
   const days=[{date:'2026-11-13',distanceKm:16.55,drivingSeconds:2525,observed:true,sampleCount:3922},{date:'2026-11-14',distanceKm:57.62,drivingSeconds:5335,observed:true,sampleCount:6874}]
   const hours=Array.from({length:24},(_,i)=>({from:new Date(since+i*3600000).toISOString(),to:new Date(since+(i+1)*3600000).toISOString(),distanceKm:i===4?12:null,drivingSeconds:i===4?600:null,observed:i===4}))
   data=observation({range:{period:rolling?'24h':'7',from:rolling?'2026-11-14':'2026-11-13',to:'2026-11-15',fromAt:new Date(since).toISOString(),toAt:new Date(now).toISOString()},days,hours:rolling?hours:undefined,period:{hasData:true,distanceKm:rolling?12:74.17,drivingSeconds:rolling?600:7860},trips:[],sourceFiles:2,unreadableFiles:0,noFixSentences:0,badChecksums:0})
  }else if(path.endsWith('/recordings/track'))data=observation({date:url.searchParams.get('date'),source:'ncc-retained-measured-positions',points:[{at:'2026-11-14T01:00:00Z',lat:35,lon:139,segment:0},{at:'2026-11-14T01:00:01Z',lat:35,lon:139.0001,segment:0}],segments:[{id:0}]})
  await route.fulfill({json:data})
 })
 return requests
}

for(const width of [1000,1200])test(`${width}px dashboard defaults to seven days and keeps six panels at the bottom`,async({page},info)=>{
 await page.setViewportSize({width,height:1800});const requests=await seed(page);await page.goto('/#dashboard')
 await expect(page.getByTestId('trip-distance')).toHaveText('74.2km')
 expect(requests[0]).toBe('?period=7')
 await expect(page.getByRole('button',{name:'7日',exact:true})).toHaveAttribute('aria-pressed','true')
 await page.getByRole('button',{name:'24時間',exact:true}).click()
 await expect(page.getByTestId('trip-distance')).toHaveText('12km')
 await expect(page.locator('.jc-bucket')).toHaveCount(24)
 await expect(page.locator('.jc-hour-tick')).toHaveText(['12','15','18','21','0','3','6','9'])
 await expect(page.getByText('過去24時間',{exact:true})).toBeVisible()
 await page.clock.runFor(500)
 const geometry=await page.locator('.dash-home').evaluate(root=>{const rect=(selector:string)=>root.querySelector(selector)!.getBoundingClientRect();const parent=root.getBoundingClientRect();return {bottomGap:parent.bottom-Math.max(rect('.device-status').bottom,rect('.resource-monitor').bottom),journeyTop:rect('.dh-journey').top,mapBottom:rect('.dh-window').bottom}})
 expect(geometry.bottomGap).toBeLessThanOrEqual(18);expect(geometry.bottomGap).toBeGreaterThanOrEqual(14)
 expect(geometry.journeyTop).toBeGreaterThanOrEqual(geometry.mapBottom)
 const button=page.getByRole('button',{name:'現在地に戻る',exact:true})
 await expect(button).toBeVisible();expect(await button.textContent()).toBe('')
 const b=(await button.boundingBox())!,credit=(await page.locator('.current-location-credit').boundingBox())!
 expect(Math.abs(b.x+b.width-credit.x-credit.width)).toBeLessThan(2)
 expect(credit.y-b.y-b.height).toBeGreaterThanOrEqual(3);expect(credit.y-b.y-b.height).toBeLessThan(25)
 await page.screenshot({path:info.outputPath('recent-'+width+'.png')})
})

test('24-hour ticks use whole JST hours every three hours, including day endpoints',()=>{
 const ticks=rollingHourTicks('2026-11-14T15:00:00Z','2026-11-15T15:00:00Z')
 expect(ticks.map(tick=>tick.label)).toEqual(['0','3','6','9','12','15','18','21','24'])
 expect(ticks[0].position).toBe(0);expect(ticks.at(-1)!.position).toBe(1)
 const shifted=rollingHourTicks('2026-11-14T20:17:00Z','2026-11-15T20:17:00Z')
 expect(shifted.map(tick=>tick.label)).toEqual(['6','9','12','15','18','21','0','3'])
})

test('daily grouping stays beside Custom instead of wrapping in narrow dashboard cards',async({page})=>{
 await seed(page);await page.goto('/#dashboard')
 for(const width of [320,390,1000,1200,1440,2618]){
  await page.setViewportSize({width,height:1000})
  // Crossing the mobile breakpoint replaces the shell and its chart. Resolve
  // the controls again once that responsive render has settled.
  await expect(async()=>{
   const custom=page.getByRole('button',{name:'カスタム',exact:true}),group=page.getByLabel('走行グラフの集約')
   await custom.scrollIntoViewIfNeeded()
   const c=(await custom.boundingBox())!,g=(await group.boundingBox())!
   expect(Math.abs(c.y+c.height/2-g.y-g.height/2)).toBeLessThan(3)
   expect(g.x-c.x-c.width).toBeGreaterThanOrEqual(0)
  }).toPass()
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true)
 }
})

test('past recorded days remain selectable and retain their measured totals',async({page})=>{
 await seed(page);await page.goto('/#trips')
 for(const date of ['2026-11-13','2026-11-14']){
  await page.getByLabel('地図に表示する日付').fill(date)
  await expect(page.getByLabel('詳細を表示する日付')).toHaveValue(date)
  await expect.poll(()=>overlayCount(page,'.journey-map .radio-map-canvas','journey')).toBe(1)
  await expect(page.locator('.dh-day-values')).toContainText(date.endsWith('13')?'16.6':'57.6')
 }
})

test('dashboard draws a detailed rolling route across midnight as a line',async({page})=>{
 await page.setViewportSize({width:1200,height:1800});await seed(page)
 const start=Date.parse('2026-11-14T14:50:00Z')
 const points=Array.from({length:1200},(_,i)=>({at:new Date(start+i*1000).toISOString(),lat:35+i*.000001+Math.sin(i/10)*.0001,lon:139+i*.000004,segment:0}))
 await page.route('**/api/v1/recordings/track?*',async route=>{
  const q=new URL(route.request().url()).searchParams
  expect(Date.parse(q.get('to')!)-Date.parse(q.get('from')!)).toBe(86400000)
  await route.fulfill({json:observation({points,segments:[{id:0}],source:'synthetic-detailed-track'})})
 })
 await page.goto('/#dashboard')
 // Dots are SVG paths too; require actual line commands and a single route
 // through JST midnight instead of accepting any path as a recovered line.
 await expect.poll(()=>overlayCount(page,'.current-location-canvas','current-trail')).toBe(1)
 expect((await vectorState(page,'.current-location-canvas'))!.sources['current-trail'].features[0].geometry.coordinates).toHaveLength(1200)
})

for(const viewport of [{width:1440,height:900},{width:2618,height:1886}])test(`map credit and modern locate button align to the actual right edge at ${viewport.width}px`,async({page},info)=>{
 await page.setViewportSize(viewport);await seed(page);await page.goto('/#dashboard');await page.clock.runFor(600)
 const button=page.getByRole('button',{name:'現在地に戻る',exact:true}),credit=page.locator('.current-location-credit'),frame=page.locator('.current-location-frame')
 await expect(button).toBeVisible();await expect(button.locator('svg')).toHaveAttribute('fill','currentColor')
 const b=(await button.boundingBox())!,c=(await credit.boundingBox())!,f=(await frame.boundingBox())!
 expect(f.x+f.width-b.x-b.width).toBeCloseTo(12,0)
 expect(f.x+f.width-c.x-c.width).toBeCloseTo(12,0)
 expect(c.y-b.y-b.height).toBeGreaterThanOrEqual(3);expect(c.y-b.y-b.height).toBeLessThan(25)
 await button.click()
 await page.screenshot({path:info.outputPath('map-right-edge-'+viewport.width+'.png')})
})
