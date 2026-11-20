import {expect,test} from '@playwright/test'
import type {PicsAxes,PicsSettings,PicsSignal} from '../src/services/pics'

const base=process.env.NCC_UI_REVIEW_URL
test.skip(!base,'Local Vite fixtures required')

for(const width of [1440,390])test(`高度化PICS dashboard and VOICEVOX settings (${width}px)`,async({page})=>{
 await page.setViewportSize({width,height:900})
 await page.addInitScript(()=>{localStorage.setItem('ncc.dashboard','overview');(window as any).EventSource=class extends EventTarget{close(){}}})
 let settings:PicsSettings={enabled:true,speech:false,style:1,volume:60}
 let signals:PicsSignal[]=[{id:'00000001',slot:3,color:'red',seconds:27}]
 let previews=0
 let axes:PicsAxes=[null,null,null,null,null,null]
 await page.route('https://tiles.openfreemap.org/**',route=>route.abort())
 await page.route('**/api/v1/**',async route=>{
  const {pathname}=new URL(route.request().url()),method=route.request().method()
  let json:unknown={}
  switch(pathname){
   case '/api/v1/settings': json={revision:1,theme:'light',locale:'ja',favorites:[]};break
   case '/api/v1/auth/session': json={csrf:'fixture'};break
   case '/api/v1/pics':
    if(method==='PUT'){settings=route.request().postDataJSON();json=settings}
    else json={settings,course:90,state:settings.enabled?'scanning':'off',signals:settings.enabled?signals.map(s=>({...s,directionMatch:settings.directional&&axes[s.slot-1]===90})):[]}
    break
   case '/api/v1/pics/speech':json={ready:true,prepared:true,styles:[{id:1,speaker:'ずんだもん',name:'あまあま'},{id:3,speaker:'ずんだもん',name:'ノーマル'}]};break
   case '/api/v1/pics/speech/preview':previews++;json={played:true};break
   case '/api/v1/pics/history':json={date:new URL(route.request().url()).searchParams.get('date'),entries:Array.from({length:5},(_,i)=>({id:'00000001',name:'試験交差点',slot:3,color:'green',at:`2026-11-21T04:0${i}:00Z`,position:{lat:20+i*.0001,lon:100},beacon:{lat:20,lon:100.0002},course:90,previousColor:'red',phaseSeconds:24,cycleSeconds:59+i*.4})),incomplete:false};break
   case '/api/v1/pics/directions':json={'00000001':axes};break
   case '/api/v1/pics/directions/00000001':axes=route.request().postDataJSON().axes;json=axes;break
  }
  await route.fulfill({json})
 })
 await page.goto(base!)
 const item=page.getByRole('link',{name:'信号（高度化PICS）の設定'})
 await expect(item).toContainText('青まであと27秒')
 await expect(item).toContainText('目安')
 const itemBox=(await item.boundingBox())!
 const cameraBox=(await page.locator('.dashboard-camera-pair').boundingBox())!
 expect(itemBox.y).toBeGreaterThanOrEqual(cameraBox.y+cameraBox.height-1)
 signals=[{id:'00000001',slot:3,color:'red',seconds:null}]
 await expect(item).toContainText('赤・残り時間不明')
 await item.click()
 const panel=page.getByRole('region',{name:'高度化PICS（実験機能）'})
 const enable=panel.getByRole('checkbox',{name:'信号をダッシュボードに表示する'})
 const voice=panel.getByRole('checkbox',{name:'VOICEVOXで本体から発話する'})
 const directional=panel.getByRole('checkbox',{name:'進行方向で絞り込む'})
 await expect(enable).toBeChecked()
 await expect(voice).not.toBeChecked()
 await expect(directional).not.toBeChecked()
 await voice.check()
 await panel.getByRole('button',{name:'保存',exact:true}).click()
 await panel.getByRole('combobox',{name:'声',exact:true}).selectOption('3')
 await panel.getByRole('button',{name:'保存',exact:true}).click()
 await panel.getByRole('button',{name:'本体で試聴'}).click()
 await expect(panel.getByRole('status')).toHaveText('本体で再生しました')
 await expect(panel.getByText('読み上げ音声を準備済み')).toBeVisible()
 expect(previews).toBe(1)
 expect(settings).toMatchObject({speech:true,style:3,volume:60})
 await directional.check()
 await panel.getByRole('button',{name:'保存',exact:true}).click()
 await expect(panel.getByText('進行方向 東 90°。', {exact:false})).toBeVisible()
 await panel.getByText('受信記録・地図・周期').click()
 await expect(panel.getByRole('region',{name:'高度化PICSの受信地点地図'})).toHaveAttribute('data-features-pics-points','6')
 await expect(panel.getByRole('cell',{name:'約59.8秒'})).toBeVisible()
 await panel.getByRole('combobox',{name:'交差点',exact:true}).selectOption('00000001')
 await panel.getByText('この交差点の横断方向を登録').click()
 await panel.getByRole('spinbutton',{name:'信号3の方位'}).fill('90')
 await panel.getByRole('button',{name:'横断方向を保存'}).click()
 await expect(panel.getByText('横断方向を保存しました')).toBeVisible()
 expect(axes[2]).toBe(90)
 await panel.getByText('受信記録・地図・周期').click()
 await enable.uncheck()
 await panel.getByRole('button',{name:'保存',exact:true}).click()
 await expect(panel.getByRole('status')).toHaveText('保存しました')
 await page.goto(base!)
 await expect(page.locator('.dashboard-camera-pair')).toBeVisible()
 await expect(item).toHaveCount(0)
})
