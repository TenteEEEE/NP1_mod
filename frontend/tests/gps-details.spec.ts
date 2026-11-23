import {test,expect} from '@playwright/test'
import {vectorTiles} from './fixtures/vector-tiles'
const url=process.env.NCC_UI_REVIEW_URL?.replace(/\/$/,'')
const stamp='2026-11-24T00:00:00Z'
const obs=(data:unknown)=>({status:'available',data,source:'synthetic-gps-fixture',observedAt:stamp,refreshSeconds:2,verified:true})

test.describe('GPS details display',()=>{
 test.skip(!url,'Set NCC_UI_REVIEW_URL to the local Vite UI')
 test('shows a responsive map, quality values and browser-rendered satellite view',async({page})=>{
  let writes=0;const errors:string[]=[];page.on('pageerror',e=>errors.push(e.message));await vectorTiles(page)
  await page.addInitScript(()=>{(window as any).EventSource=class extends EventTarget{onerror=null;close(){}}})
  await page.route('**/api/v1/**',async route=>{const req=route.request(),path=new URL(req.url()).pathname;if(req.method()!=='GET'){writes++;return route.fulfill({status:409,json:{reason:'read-only fixture'}})}
   let data:unknown={};if(path.endsWith('/auth/session'))data={csrf:'fixture'}
   else if(path.endsWith('/settings'))data={revision:1,theme:'light',locale:'ja',favorites:[]}
   else if(path.endsWith('/system/summary'))data={gps:obs({fix:true,live:true,at:stamp,lat:35,lon:139,satellites:12,accuracyM:4,hdop:.8,qualityAgeSeconds:3,positionSource:'stock-image-recognition'})}
   else if(path.endsWith('/device/gps/live'))data=obs({fix:true,live:true,at:stamp,lat:35,lon:139,satellites:12,accuracyM:4,hdop:.8,qualityAgeSeconds:3,positionSource:'stock-image-recognition'})
   else if(path.includes('/trips/track'))data={segments:[]}
   await route.fulfill({json:data})})
  for(const width of [390,1440]){await page.setViewportSize({width,height:900});await page.goto(url+'/#system/gps');await expect(page.getByRole('heading',{name:'GPS詳細'})).toBeVisible();await expect(page.getByText('12',{exact:true})).toBeVisible();await expect(page.getByText('4 m',{exact:true})).toBeVisible();await expect(page.getByText('HDOP 0.8',{exact:true})).toBeVisible();await expect(page.getByRole('region',{name:'現在地の地図'})).toBeVisible();const canvas=page.getByLabel('受信衛星数を示す3D地球儀');await expect(canvas).toBeVisible();await expect(page.getByText(/点の位置は実際の衛星軌道/)).toBeVisible();expect(await canvas.evaluate((el:HTMLCanvasElement)=>el.width>0&&el.height>0)).toBe(true);expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true)}
  expect(writes).toBe(0);expect(errors).toEqual([])
 })
})
