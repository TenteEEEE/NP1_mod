import {test,expect} from '@playwright/test'
import {readFileSync} from 'node:fs'

test.describe('journey map on the installed device',()=>{
 test.skip(process.env.NCC_JOURNEY_HARDWARE!=='1','Explicit installed-device check required')

 test('restored detailed track retains its source points and renders lines',async({page,request},info)=>{
  const file=process.env.NCC_JOURNEY_REFERENCE_TRACK
  test.skip(!file,'A private measured reference export is required')
  const reference=JSON.parse(readFileSync(file!,'utf8')).data
  expect(reference.points.length).toBeGreaterThan(1000)
  const latitudes=reference.points.map((point:any)=>point.lat),longitudes=reference.points.map((point:any)=>point.lon)
  expect(Math.max(...latitudes)-Math.min(...latitudes)+Math.max(...longitudes)-Math.min(...longitudes),'Use an actual driving route; stationary GPS drift does not verify route recovery').toBeGreaterThan(.01)
  const response=await request.get('/api/v1/recordings/track?date='+reference.date)
  expect(response.ok()).toBe(true)
  const actual=(await response.json()).data
  const lookup=new Map<string,any>(actual.points.map((point:any)=>[point.at,point]))
  for(let i=0;i<reference.points.length;i++){
   const point=reference.points[i],restored=lookup.get(point.at)
   expect(restored?.lat).toBe(point.lat);expect(restored?.lon).toBe(point.lon)
   if(i&&reference.points[i-1].segment===point.segment)expect(restored.segment).toBe(lookup.get(reference.points[i-1].at)?.segment)
  }
  await page.goto('/#trips')
  await page.getByLabel('地図に表示する日付').fill(reference.date)
  // Circle markers also use SVG paths. Requiring L commands verifies actual
  // polylines instead of considering a handful of isolated dots a success.
  await expect.poll(()=>page.locator('.journey-route').evaluateAll(nodes=>nodes.reduce((count,node)=>count+(node.getAttribute('d')?.match(/L/g)?.length||0),0))).toBeGreaterThan(100)
  await expect(page.locator('.journey-map-status')).toContainText(actual.points.length.toLocaleString())
  await page.locator('.dh-radio').screenshot({path:info.outputPath('restored-detailed-route.private.png')})
 })

 test('November 13 and 14 show saved driving totals and measured GPS on their maps',async({page,request},info)=>{
  const history=await request.get('/api/v1/trips?period=custom&from=2026-11-13&to=2026-11-14')
  expect(history.ok()).toBe(true);const days=(await history.json()).data.days
  await page.goto('/#trips')
  for(const date of ['2026-11-13','2026-11-14']){
   const day=days.find((entry:any)=>entry.date===date);expect(day?.observed).toBe(true);expect(day.distanceKm).toBeGreaterThan(0)
   const track=await request.get('/api/v1/recordings/track?date='+date);expect(track.ok()).toBe(true);expect((await track.json()).data.points.length).toBeGreaterThan(0)
   await page.getByLabel('地図に表示する日付').fill(date)
   await expect(page.getByLabel('詳細を表示する日付')).toHaveValue(date)
   await expect(page.locator('.journey-route').first()).toBeAttached()
   await expect(page.locator('.dh-day-values')).toContainText(day.distanceKm.toLocaleString('ja-JP',{maximumFractionDigits:1}))
   await page.locator('.dh-radio').screenshot({path:info.outputPath(date+'.private.png')})
  }
 })

 test('recorded GPS route survives radio toggles, panning and mobile layout',async({page,request},info)=>{
  const errors:string[]=[];page.on('pageerror',error=>errors.push(error.message))
  const recordings=await request.get('/api/v1/recordings');expect(recordings.ok()).toBe(true)
  const dates=(await recordings.json()).data.days.map((day:any)=>typeof day==='string'?day:day.date).sort()
  expect(dates.length).toBeGreaterThan(0)
  const date=dates.at(-1)!
  const response=await request.get('/api/v1/recordings/track?date='+date);expect(response.ok()).toBe(true)
  const track=(await response.json()).data
  expect(track.points.length).toBeGreaterThan(0)
  await page.goto('/#trips')
  await page.getByLabel('地図に表示する日付').fill(date)
  await expect(page.locator('.journey-route').first()).toBeAttached()
  await expect(page.getByText('地図に表示できる測位がありません',{exact:true})).toHaveCount(0)
  const order=await page.locator('.drive-history>section').evaluateAll(nodes=>nodes.map(node=>node.querySelector('h2')?.textContent))
  expect(order).toEqual(['走行の記録','走行履歴マップ','日別の詳細'])
  const toggle=page.getByLabel('電波強度のヒートマップを表示');await expect(toggle).not.toBeChecked()
  await toggle.check();await expect(page.locator('.radio-overlay').first()).toBeAttached()
  await expect(page.locator('.journey-route').first()).toBeAttached()
  await page.locator('.dh-radio').screenshot({path:info.outputPath('journey-radio-overlay.private.png')})
  await toggle.uncheck();await expect(page.locator('.radio-overlay')).toHaveCount(0)
  await expect(page.locator('.journey-route').first()).toBeAttached()
  const canvas=page.locator('.journey-map .radio-map-canvas');await canvas.scrollIntoViewIfNeeded()
  const box=(await canvas.boundingBox())!,pane=canvas.locator('.leaflet-map-pane'),before=await pane.getAttribute('style')
  await page.mouse.move(box.x+box.width/2,box.y+box.height/2);await page.mouse.down();await page.mouse.move(box.x+box.width/2+100,box.y+box.height/2+40,{steps:12});await page.mouse.up()
  expect(await pane.getAttribute('style')).not.toBe(before)
  await page.getByRole('button',{name:'全体を表示',exact:true}).click()
  await page.locator('.dh-radio').screenshot({path:info.outputPath('journey-desktop.private.png')})
  await page.setViewportSize({width:390,height:844});await page.getByRole('heading',{name:'走行履歴マップ',exact:true}).scrollIntoViewIfNeeded()
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true)
  await expect(page.getByLabel('詳細を表示する日付')).toHaveValue(date)
  await expect(page.locator('.journey-route').first()).toBeAttached()
  await page.locator('.dh-radio').screenshot({path:info.outputPath('journey-mobile.private.png')})
  expect(errors).toEqual([])
 })

 test('overview uses a moving 24-hour window and keeps manual panning through GPS updates',async({page},info)=>{
  await page.addInitScript(()=>{localStorage.setItem('ncc.dashboard','overview');localStorage.setItem('ncc.nav','float')})
  await page.setViewportSize({width:1440,height:900})
  const tracks:URLSearchParams[]=[]
  page.on('request',request=>{const url=new URL(request.url());if(url.pathname.endsWith('/recordings/track'))tracks.push(url.searchParams)})
  await page.goto('/#dashboard')
  await expect(page.getByRole('button',{name:'7日',exact:true})).toHaveAttribute('aria-pressed','true')
  await page.getByRole('button',{name:'24時間',exact:true}).click()
  await expect(page.getByRole('button',{name:'24時間',exact:true})).toHaveAttribute('aria-pressed','true')
  await expect.poll(()=>page.locator('.jc-hour-tick').count()).toBeGreaterThanOrEqual(8)
  const tickLabels=await page.locator('.jc-hour-tick').allTextContents()
  expect(tickLabels.length).toBeLessThanOrEqual(9)
  expect(tickLabels.every(label=>/^\d+$/.test(label)&&Number(label)%3===0)).toBe(true)
  const period=await page.request.get('/api/v1/trips?period=24h');expect(period.ok()).toBe(true)
  const report=(await period.json()).data;expect(report.hours).toHaveLength(24)
  expect(Date.parse(report.range.toAt)-Date.parse(report.range.fromAt)).toBe(86400000)
  await expect.poll(()=>tracks.length).toBeGreaterThan(0)
  const query=tracks[0]!
  expect(Date.parse(query.get('to')!)-Date.parse(query.get('from')!)).toBe(86400000)
  expect(query.has('date')).toBe(false)
  await expect(page.locator('.current-location-trail').first()).toBeAttached()
  const box=(await page.locator('.dh-window').boundingBox())!,x=box.x+box.width/2,y=box.y+box.height/2
  const pane=page.locator('.current-location-canvas .leaflet-map-pane'),before=await pane.getAttribute('style')
  await page.mouse.move(x,y);await page.mouse.down();await page.mouse.move(x+100,y+40,{steps:12});await page.mouse.up()
  const resume=page.getByRole('button',{name:/現在地に戻る|最後の位置に戻る/})
  await expect(resume).toBeVisible();expect(await pane.getAttribute('style')).not.toBe(before)
  expect(await resume.textContent()).toBe('')
  const buttonRect=(await resume.boundingBox())!,credit=(await page.locator('.current-location-credit').boundingBox())!
  const mapFrame=(await page.locator('.current-location-frame').boundingBox())!
  expect(mapFrame.x+mapFrame.width-buttonRect.x-buttonRect.width).toBeCloseTo(12,0)
  expect(mapFrame.x+mapFrame.width-credit.x-credit.width).toBeCloseTo(12,0)
  expect(Math.abs(buttonRect.x+buttonRect.width-credit.x-credit.width)).toBeLessThan(2)
  expect(credit.y-buttonRect.y-buttonRect.height).toBeGreaterThanOrEqual(3)
  expect(credit.y-buttonRect.y-buttonRect.height).toBeLessThan(25)
  // Let Leaflet's drag inertia settle before checking that GPS does not recenter it.
  await page.waitForTimeout(600)
  const moved=await pane.getAttribute('style');await page.waitForTimeout(2500);expect(await pane.getAttribute('style')).toBe(moved)
  await resume.click();await expect(resume).toHaveAttribute('aria-pressed','true')
  await page.screenshot({path:info.outputPath('dashboard-24h.private.png')})
 })

 test('medium desktop widths keep the six real panels near the bottom',async({page},info)=>{
  await page.addInitScript(()=>{localStorage.setItem('ncc.dashboard','overview');localStorage.setItem('ncc.nav','float')})
  for(const width of [1000,1200]){
   await page.setViewportSize({width,height:1800});await page.goto('/#dashboard')
   await expect(page.getByRole('button',{name:'7日',exact:true})).toHaveAttribute('aria-pressed','true')
   await expect(page.locator('.jc-bucket')).toHaveCount(7)
   await expect.poll(()=>page.locator('.dash-home').evaluate(root=>{const end=Math.max(...[...root.querySelectorAll('.dh-tiles>.panel')].map(node=>node.getBoundingClientRect().bottom));return Math.round(root.getBoundingClientRect().bottom-end)})).toBeLessThanOrEqual(18)
   expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true)
   await page.screenshot({path:info.outputPath('medium-'+width+'.private.png')})
  }
 })

 for(const width of [1440,2618])test(`map attribution and navigation button stay at the right edge on the device at ${width}px`,async({page},info)=>{
  await page.addInitScript(()=>{localStorage.setItem('ncc.dashboard','overview');localStorage.setItem('ncc.nav','float')})
  await page.setViewportSize({width,height:width===2618?1886:900})
  await page.goto('/#dashboard')
  const button=page.getByRole('button',{name:/現在地に戻る|最後の位置に戻る/}),credit=page.locator('.current-location-credit')
  await expect(button).toBeVisible();await expect(button.locator('svg')).toHaveAttribute('fill','currentColor')
  await expect.poll(async()=>{
   const b=await button.boundingBox(),c=await credit.boundingBox(),f=await page.locator('.current-location-frame').boundingBox()
   return b&&c&&f?Math.max(Math.abs(f.x+f.width-b.x-b.width-12),Math.abs(f.x+f.width-c.x-c.width-12)):1000
  }).toBeLessThan(2)
  await button.click()
  const b=(await button.boundingBox())!,c=(await credit.boundingBox())!
  expect(c.y-b.y-b.height).toBeGreaterThanOrEqual(3);expect(c.y-b.y-b.height).toBeLessThan(25)
  await page.screenshot({path:info.outputPath('right-edge-'+width+'.private.png')})
 })

 test.describe('phone touch',()=>{
  test.use({hasTouch:true})
  test('one finger moves the map and return button is usable beside the live thumbnail',async({page},info)=>{
   await page.emulateMedia({reducedMotion:'reduce'})
   await page.addInitScript(()=>{localStorage.setItem('ncc.dashboard','overview');localStorage.setItem('ncc.nav','float')})
   await page.setViewportSize({width:390,height:844});await page.goto('/#dashboard')
   await expect(page.locator('.current-location-trail').first()).toBeAttached()
   const box=(await page.locator('.dh-window').boundingBox())!,x=box.x+box.width/2,y=box.y+box.height/2
   const session=await page.context().newCDPSession(page)
   await session.send('Input.dispatchTouchEvent',{type:'touchStart',touchPoints:[{x,y,id:1}]})
   for(let i=1;i<=10;i++)await session.send('Input.dispatchTouchEvent',{type:'touchMove',touchPoints:[{x:x+8*i,y:y+2*i,id:1}]})
   await session.send('Input.dispatchTouchEvent',{type:'touchEnd',touchPoints:[]})
   const resume=page.getByRole('button',{name:/現在地に戻る|最後の位置に戻る/})
   await expect(resume).toBeVisible();await resume.click();await expect(resume).toHaveAttribute('aria-pressed','true')
   expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true)
   await page.screenshot({path:info.outputPath('dashboard-touch.private.png')})
  })
 })
})
