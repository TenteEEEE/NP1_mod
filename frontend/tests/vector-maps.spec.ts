import {test,expect} from '@playwright/test'
import {vectorStyle} from '../src/services/vector-style'
import {validateStyleMin} from '@maplibre/maplibre-gl-style-spec'
import {vectorTiles,vectorState} from './fixtures/vector-tiles'

test('all map palettes are valid vector-only styles with zoom-dependent detail',()=>{
 for(const palette of ['mist','night','sage','colorful'] as const){
  const style=vectorStyle(palette)
  expect(validateStyleMin(style).map(error=>error.message)).toEqual([])
  expect(validateStyleMin(vectorStyle(palette,false)).map(error=>error.message)).toEqual([])
  expect(Object.values(style.sources).every(source=>source.type==='vector')).toBe(true)
  expect(style.layers.find(layer=>layer.id==='buildings')?.minzoom).toBe(14)
  expect(style.layers.find(layer=>layer.id==='poi-names')?.minzoom).toBe(15)
  expect(style.layers.find(layer=>layer.id==='addresses')?.minzoom).toBe(17)
 }
})

test('dashboard renders real vector tiles, changes detail with zoom and retains its GPS geometry',async({page},info)=>{
 const errors:string[]=[];page.on('pageerror',error=>errors.push(error.message))
 const requests=await vectorTiles(page)
 const now=new Date().toISOString()
 await page.addInitScript(()=>{localStorage.setItem('ncc.dashboard','overview');localStorage.setItem('ncc.nav','float');(window as any).EventSource=class extends EventTarget{close(){}}})
 await page.route('**/api/v1/**',async route=>{
  const path=new URL(route.request().url()).pathname;let data:unknown={}
  if(path.endsWith('/auth/session'))data={csrf:'fixture'}
  else if(path.endsWith('/settings'))data={revision:1,theme:'light',locale:'ja',favorites:[],mapThemes:{dashboard:'colorful',journey:'night',playback:'mist',plates:'sage'}}
  else if(path.endsWith('/system/summary'))data={gps:{status:'available',data:{fix:true,at:now,lat:35,lon:139}}}
  else if(path.endsWith('/device/gps/live'))data={status:'available',data:{fix:true,at:now,lat:35,lon:139}}
  else if(path.endsWith('/recordings/track'))data={status:'available',data:{points:[{at:now,lat:35,lon:139,segment:0},{at:now,lat:35.001,lon:139.001,segment:0}],segments:[{id:0}]}}
  else if(path.endsWith('/trips'))data={data:{range:{period:'7',from:'2026-11-10',to:'2026-11-16'},days:[],period:{hasData:false},trips:[]}}
  await route.fulfill({json:data})
 })
 await page.goto('/#dashboard')
 const selector='.current-location-canvas'
 await expect(page.locator(selector)).toHaveAttribute('data-map-ready','true')
 await expect(page.locator(selector)).toHaveAttribute('data-map-theme','colorful')
 await expect.poll(()=>requests.filter(url=>url.includes('/test/')).length).toBeGreaterThan(0)
 await expect.poll(async()=>(await vectorState(page,selector))?.rendered.filter((f:any)=>f.layer==='buildings').length||0).toBeGreaterThan(0)
 const detailed=(await vectorState(page,selector))!
 expect(detailed.sources['current-trail'].features[0].geometry.coordinates).toEqual([[139,35],[139.001,35.001]])
 for(const palette of ['night','sage','mist','colorful']){
  await page.evaluate(value=>document.documentElement.setAttribute('data-map-dashboard-theme',value),palette)
  await expect(page.locator(selector)).toHaveAttribute('data-map-theme',palette)
  await expect(page.locator(selector)).toHaveAttribute('data-map-ready','true')
  expect((await vectorState(page,selector))!.sources['current-trail']).toEqual(detailed.sources['current-trail'])
  expect((await vectorState(page,selector))!.center).toEqual(detailed.center)
  await page.screenshot({path:info.outputPath('vector-'+palette+'.png')})
 }
 await page.locator(selector).evaluate(async el=>{const {inspectVectorMap}=await import('/src/services/vector-map.ts' as string);inspectVectorMap(el as HTMLElement)!.jumpTo({zoom:8})})
 await expect.poll(async()=>(await vectorState(page,selector))?.zoom).toBe(8)
 await expect.poll(async()=>(await vectorState(page,selector))!.rendered.some((f:any)=>['buildings','poi-names','addresses'].includes(f.layer))).toBe(false)
 await page.screenshot({path:info.outputPath('vector-colorful-wide.png')})
 expect(errors).toEqual([])
})

test('display settings persist independent map themes and restore them on a new page',async({page})=>{
 let settings:any={revision:1,theme:'light',locale:'ja',favorites:[]}
 const writes:any[]=[]
 await vectorTiles(page)
 await page.addInitScript(()=>{(window as any).EventSource=class extends EventTarget{close(){}}})
 await page.route('**/api/v1/**',async route=>{
  const path=new URL(route.request().url()).pathname;let data:unknown={}
  if(path.endsWith('/auth/session'))data={csrf:'fixture'}
  else if(path.endsWith('/settings')){
   if(route.request().method()==='PUT'){const body=route.request().postDataJSON();writes.push(body);settings={...body.settings,revision:settings.revision+1}}
   data=settings
  }
  await route.fulfill({json:data})
 })
 await page.goto('/#system/settings')
 const choices={'ダッシュボード':'colorful','走行履歴':'night','録画再生':'mist','ナンバー記録':'sage'}
 for(const label of Object.keys(choices))await expect(page.getByRole('combobox',{name:label+'の地図',exact:true})).toHaveValue('auto')
 for(const [label,value]of Object.entries(choices))await page.getByRole('combobox',{name:label+'の地図',exact:true}).selectOption(value)
 await page.getByRole('button',{name:'変更を確認',exact:true}).click()
 const dialog=page.getByRole('dialog',{name:'表示設定の変更'})
 await expect(dialog).toContainText('カラー · 標準地図風')
 await dialog.getByRole('button',{name:'変更を適用'}).click()
 await expect(dialog).toHaveCount(0)
 expect(writes).toHaveLength(1)
 expect(writes[0].settings.mapThemes).toEqual({dashboard:'colorful',journey:'night',playback:'mist',plates:'sage'})
 await page.reload()
 for(const [label,value]of Object.entries(choices))await expect(page.getByRole('combobox',{name:label+'の地図',exact:true})).toHaveValue(value)
 expect(await page.locator('html').evaluate(el=>['dashboard','journey','playback','plates'].map(view=>el.getAttribute('data-map-'+view+'-theme')))).toEqual(['colorful','night','mist','sage'])
 await page.goto('/#trips')
 await expect(page.locator('.radio-map-canvas')).toHaveAttribute('data-map-theme','night')
})

test('failed vector downloads keep the measured route visible',async({page})=>{
 await vectorTiles(page,true)
 const now=new Date().toISOString()
 await page.addInitScript(()=>{(window as any).EventSource=class extends EventTarget{close(){}}})
 await page.route('**/api/v1/**',route=>{
  const path=new URL(route.request().url()).pathname
  const data=path.endsWith('/auth/session')?{csrf:'fixture'}:path.endsWith('/settings')?{revision:1,theme:'light'}:path.endsWith('/recordings/track')?{data:{date:'2026-11-16',points:[{at:now,lat:35,lon:139,segment:0},{at:now,lat:35.01,lon:139.01,segment:0}],segments:[{id:0}]}}:{}
  return route.fulfill({json:data})
 })
 await page.goto('/#trips')
 const selector='.radio-map-canvas'
 await expect(page.getByText('背景地図を読み込めませんでした。走行経路は引き続き表示します。')).toBeVisible()
 await expect.poll(async()=>(await vectorState(page,selector))?.rendered.filter((f:any)=>f.layer==='journey-line').length||0).toBeGreaterThan(0)
 const route=(await vectorState(page,selector))!.sources.journey
 await page.getByLabel('背景地図を表示').uncheck()
 await expect(page.locator(selector)).toHaveAttribute('data-map-ready','true')
 expect((await vectorState(page,selector))!.sources.journey).toEqual(route)
})
