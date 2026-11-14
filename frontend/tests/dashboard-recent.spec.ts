import {test,expect,type Page} from '@playwright/test'
const now=Date.parse('2026-11-15T00:30:00Z'),since=now-86400000
const observation=(data:unknown)=>({status:'available',data,source:'synthetic-recent-dashboard',verified:false})
async function seed(page:Page){
 await page.clock.install({time:new Date(now)});await page.clock.pauseAt(new Date(now))
 await page.emulateMedia({reducedMotion:'reduce'})
 await page.addInitScript(()=>{localStorage.setItem('ncc.dashboard','overview');localStorage.setItem('ncc.nav','float');(window as any).EventSource=class extends EventTarget{close(){}}})
 const requests:string[]=[]
 await page.route('https://tile.openstreetmap.org/**',route=>route.abort())
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

for(const width of [1000,1200])test(`${width}px dashboard has rolling 24 hours and six panels at the bottom`,async({page},info)=>{
 await page.setViewportSize({width,height:1800});const requests=await seed(page);await page.goto('/#dashboard')
 await expect(page.getByTestId('trip-distance')).toHaveText('12km')
 expect(requests[0]).toBe('?period=24h')
 await expect(page.locator('.jc-bucket')).toHaveCount(24)
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

test('past recorded days remain selectable and retain their measured totals',async({page})=>{
 await seed(page);await page.goto('/#trips')
 for(const date of ['2026-11-13','2026-11-14']){
  await page.getByLabel('地図に表示する日付').fill(date)
  await expect(page.getByLabel('詳細を表示する日付')).toHaveValue(date)
  await expect(page.locator('.journey-route')).toHaveCount(1)
  await expect(page.locator('.dh-day-values')).toContainText(date.endsWith('13')?'16.6':'57.6')
 }
})
