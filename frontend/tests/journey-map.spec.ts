import {test,expect,type Page} from '@playwright/test'
const stamp='2026-11-12T03:00:00Z',now=Date.parse(stamp)
const observation=(data:unknown)=>({status:'available',data,observedAt:stamp,source:'synthetic-journey-map',verified:false})
const point=(at:string,lat:number,segment=0)=>({at,lat,lon:139,segment})
async function fixture(page:Page,overview=false){
 await page.clock.install({time:new Date(stamp)});await page.clock.pauseAt(new Date(stamp))
 await page.addInitScript(value=>{localStorage.setItem('ncc.dashboard',value?'overview':'detail');(window as any).EventSource=class extends EventTarget{close(){}}},overview)
 const calls={tracks:[] as URLSearchParams[],radio:[] as URLSearchParams[],failRadio:false}
 await page.route('https://tile.openstreetmap.org/**',route=>route.abort())
 await page.route('**/api/v1/**',async route=>{
  const url=new URL(route.request().url()),path=url.pathname;let data:unknown={}
  if(path.endsWith('/auth/session'))data={csrf:'fixture'}
  else if(path.endsWith('/settings'))data={revision:1,theme:'dark',locale:'ja',favorites:[]}
  else if(path.endsWith('/system/summary'))data={gps:observation({fix:true,at:stamp,lat:35.01,lon:139,satellites:10,accuracyM:5})}
  else if(path.endsWith('/device/gps/live'))data=observation({fix:true,at:stamp,lat:35.01,lon:139,satellites:10,accuracyM:5})
  else if(path.endsWith('/trips'))data=observation({range:{period:'7',from:'2026-11-06',to:'2026-11-12'},days:[{date:'2026-11-11',distanceKm:1,drivingSeconds:60,observed:true,sampleCount:3}],period:{hasData:true,distanceKm:1,drivingSeconds:60,validObservedDays:1,sampleCount:3},trips:[],sourceFiles:1,unreadableFiles:0,noFixSentences:0,badChecksums:0,totalTrips:0})
  else if(path.endsWith('/recordings/track')){
   calls.tracks.push(url.searchParams)
   const points=url.searchParams.has('from')?[point(new Date(now-86400001).toISOString(),34.9),point(new Date(now-86400000).toISOString(),35,1),point('2026-11-11T14:59:59Z',35.0098,2),point('2026-11-11T15:00:00Z',35.0099,2),point(stamp,35.01,3),point(new Date(now+1).toISOString(),35.02,3)]:url.searchParams.get('date')==='2026-11-11'?[point('2026-11-11T01:00:00Z',35),point('2026-11-11T01:00:01Z',35.0001),point('2026-11-11T01:01:00Z',35.001,1)]:[]
   data=observation({date:url.searchParams.get('date'),points,segments:points.length?[{id:0},{id:1}]:[]})
  }else if(path.endsWith('/radio/history')){
   calls.radio.push(url.searchParams)
   if(calls.failRadio)return route.fulfill({status:503,json:{reason:'synthetic radio failure'}})
   data={unit:'dBm',points:[],cells:[{bounds:[[35,139],[35.0002,139.0002]],color:'#247976',median:-85,sample_count:4,distinct_trip_count:1,low_confidence:false}],total:20,unplaced:20,accuracyExcluded:0,missing:0,outOfService:0,carriers:['Fixture'],bands:[],truncated:false}
  }else if(path.endsWith('/radio/heatmap/legend'))data={unit:'dBm',note:'synthetic',thresholds:[]}
  await route.fulfill({json:data})
 })
 return calls
}

for(const width of [1440,390])test(`GPS route is central and independent of optional radio at ${width}px`,async({page},info)=>{
 await page.setViewportSize({width,height:900});const calls=await fixture(page);await page.goto('/#trips')
 await expect(page.locator('.journey-route')).toHaveCount(2)
 expect(calls.radio).toHaveLength(0)
 const order=await page.locator('.drive-history>section').evaluateAll(nodes=>nodes.map(node=>node.querySelector('h2')?.textContent))
 expect(order).toEqual(['走行の記録','走行履歴マップ','日別の詳細'])
 await expect(page.getByLabel('地図に表示する日付')).toHaveValue('2026-11-11')
 await page.getByLabel('電波強度のヒートマップを表示').check();await expect(page.locator('.radio-overlay')).toHaveCount(1)
 expect(calls.radio.at(-1)?.get('date')).toBe('2026-11-11')
 await expect(page.locator('.journey-route')).toHaveCount(2)
 await page.getByLabel(/^回線/).selectOption('Fixture');await expect.poll(()=>calls.radio.at(-1)?.get('carrier')).toBe('Fixture')
 await expect(page.locator('.journey-route')).toHaveCount(2)
 await page.screenshot({path:info.outputPath('journey-map-'+width+'.png'),fullPage:true})
 await page.getByLabel('電波強度のヒートマップを表示').uncheck();await expect(page.locator('.radio-overlay')).toHaveCount(0);await expect(page.locator('.journey-route')).toHaveCount(2)
 calls.failRadio=true
 await page.getByLabel('電波強度のヒートマップを表示').check();await expect(page.getByRole('status').filter({hasText:'synthetic radio failure'})).toBeVisible();await expect(page.locator('.journey-route')).toHaveCount(2)
 await page.getByLabel('地図に表示する日付').fill('2026-11-12');await expect(page.getByLabel('詳細を表示する日付')).toHaveValue('2026-11-12')
 await expect(page.locator('.journey-route')).toHaveCount(0);await expect(page.getByText('この日の位置記録はありません',{exact:true})).toBeVisible()
 await page.getByLabel('詳細を表示する日付').fill('2026-11-11');await expect(page.locator('.journey-route')).toHaveCount(2)
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true)
})

for(const overview of [true,false])test(`${overview?'overview':'detail'} requests exactly the elapsed 24 hours and keeps the midnight segment`,async({page})=>{
 const calls=await fixture(page,overview);await page.goto('/#dashboard')
 await expect(page.locator('.current-location-trail')).toHaveCount(3)
 expect(calls.tracks[0]?.get('from')).toBe('2026-11-11T03:00:00.000Z')
 expect(calls.tracks[0]?.get('to')).toBe('2026-11-12T03:00:00.000Z')
 expect(calls.tracks[0]?.has('date')).toBe(false)
 const points=await page.locator('.current-location-trail').evaluateAll(nodes=>nodes.map(node=>({type:node.tagName,d:node.getAttribute('d')})))
 expect(points.filter(p=>p.type==='path'&&p.d?.includes('L'))).toHaveLength(1)
})

test('dragging the dashboard suspends following until returning to the current position',async({page})=>{
 await page.setViewportSize({width:1440,height:900});await fixture(page,true);await page.goto('/#dashboard')
 await expect(page.locator('.current-location-marker')).toHaveCount(1)
 const box=(await page.locator('.dh-window').boundingBox())!,x=box.x+box.width/2,y=box.y+box.height/2
 const pane=page.locator('.current-location-canvas .leaflet-map-pane'),before=await pane.getAttribute('style')
 await page.mouse.move(x,y);await page.mouse.down();await page.mouse.move(x+100,y+40,{steps:12});await page.mouse.up()
 await expect(page.getByRole('button',{name:'現在地に戻る',exact:true})).toBeVisible()
 expect(await pane.getAttribute('style')).not.toBe(before)
 const moved=await pane.getAttribute('style');await page.clock.runFor(4000);expect(await pane.getAttribute('style')).toBe(moved)
 await page.getByRole('button',{name:'現在地に戻る',exact:true}).click();await expect(page.getByRole('button',{name:'現在地に戻る',exact:true})).toHaveAttribute('aria-pressed','true')
 const centre=await page.locator('.current-location-marker').boundingBox()
 expect(Math.abs(centre!.x+centre!.width/2-x)).toBeLessThan(6);expect(Math.abs(centre!.y+centre!.height/2-y)).toBeLessThan(6)
})

test.describe('touch panning',()=>{
 test.use({hasTouch:true})
 test('a one-finger drag moves the phone map without dragging its information sheet',async({page})=>{
  await page.emulateMedia({reducedMotion:'reduce'})
  await page.setViewportSize({width:390,height:844});await fixture(page,true);await page.goto('/#dashboard')
  await expect(page.locator('.current-location-marker')).toHaveCount(1)
  await page.clock.runFor(500)
  const box=(await page.locator('.dh-window').boundingBox())!,x=box.x+box.width/2,y=box.y+box.height/2
  const pane=page.locator('.current-location-canvas .leaflet-map-pane'),before=await pane.getAttribute('style'),sheet=await page.locator('.dh-sheet').boundingBox()
  const session=await page.context().newCDPSession(page)
  await session.send('Input.dispatchTouchEvent',{type:'touchStart',touchPoints:[{x,y,id:1}]})
  for(let i=1;i<=10;i++)await session.send('Input.dispatchTouchEvent',{type:'touchMove',touchPoints:[{x:x+8*i,y:y+2*i,id:1}]})
  await session.send('Input.dispatchTouchEvent',{type:'touchEnd',touchPoints:[]})
  await expect(page.getByRole('button',{name:'現在地に戻る',exact:true})).toBeVisible()
  expect(await pane.getAttribute('style')).not.toBe(before)
  expect((await page.locator('.dh-sheet').boundingBox())!.y).toBeCloseTo(sheet!.y,0)
  await page.getByRole('button',{name:'現在地に戻る',exact:true}).click();await expect(page.getByRole('button',{name:'現在地に戻る',exact:true})).toHaveAttribute('aria-pressed','true')
 })
})

test('recorded GPS without metre accuracy still displays coloured radio points when enabled',async({page})=>{
 await fixture(page)
 await page.route('**/api/v1/radio/history?*',route=>route.fulfill({json:{unit:'dBm',points:[{at:'2026-11-11T01:00:00Z',gps:{at:'2026-11-11T01:00:00Z',lat:35,lon:139,hdop:1.2},value:-85,state:'measured',color:'#247976',source:'synthetic',accuracyUnknown:true}],cells:[],total:1,unplaced:0,accuracyExcluded:0,carriers:[],bands:[]}}))
 await page.goto('/#trips');await expect(page.locator('.journey-route')).toHaveCount(2)
 await page.getByLabel('電波強度のヒートマップを表示').check()
 await expect(page.locator('.radio-overlay')).toHaveCount(1)
 await expect(page.locator('.radio-overlay')).toHaveAttribute('stroke-dasharray','2 2')
 await expect(page.getByRole('note').filter({hasText:'破線の測定点'})).toBeVisible()
 await expect(page.locator('.journey-route')).toHaveCount(2)
})
