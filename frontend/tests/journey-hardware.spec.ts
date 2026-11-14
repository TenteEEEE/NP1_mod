import {test,expect} from '@playwright/test'

test.describe('journey map on the installed device',()=>{
 test.skip(process.env.NCC_JOURNEY_HARDWARE!=='1','Explicit installed-device check required')

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
  // Let Leaflet's drag inertia settle before checking that GPS does not recenter it.
  await page.waitForTimeout(600)
  const moved=await pane.getAttribute('style');await page.waitForTimeout(2500);expect(await pane.getAttribute('style')).toBe(moved)
  await resume.click();await expect(resume).toHaveCount(0)
  await page.screenshot({path:info.outputPath('dashboard-24h.private.png')})
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
   await expect(resume).toBeVisible();await resume.click();await expect(resume).toHaveCount(0)
   expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true)
   await page.screenshot({path:info.outputPath('dashboard-touch.private.png')})
  })
 })
})
