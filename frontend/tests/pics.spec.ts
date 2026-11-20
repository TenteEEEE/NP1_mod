import {expect,test} from '@playwright/test'
import type {PicsSettings,PicsSignal} from '../src/services/pics'

const base=process.env.NCC_UI_REVIEW_URL
test.skip(!base,'Local Vite fixtures required')

for(const width of [1440,390])test(`高度化PICS dashboard and VOICEVOX settings (${width}px)`,async({page})=>{
 await page.setViewportSize({width,height:900})
 await page.addInitScript(()=>{localStorage.setItem('ncc.dashboard','overview');(window as any).EventSource=class extends EventTarget{close(){}}})
 let settings:PicsSettings={enabled:true,speech:false,style:1,volume:60}
 let signals:PicsSignal[]=[{id:'00000001',slot:3,color:'red',seconds:27}]
 let previews=0
 await page.route('https://tiles.openfreemap.org/**',route=>route.abort())
 await page.route('**/api/v1/**',async route=>{
  const {pathname}=new URL(route.request().url()),method=route.request().method()
  let json:unknown={}
  switch(pathname){
   case '/api/v1/settings': json={revision:1,theme:'light',locale:'ja',favorites:[]};break
   case '/api/v1/auth/session': json={csrf:'fixture'};break
   case '/api/v1/pics':
    if(method==='PUT'){settings=route.request().postDataJSON();json=settings}
    else json={settings,state:settings.enabled?'scanning':'off',signals:settings.enabled?signals:[]}
    break
   case '/api/v1/pics/speech':json={ready:true,styles:[{id:1,speaker:'ずんだもん',name:'あまあま'},{id:3,speaker:'ずんだもん',name:'ノーマル'}]};break
   case '/api/v1/pics/speech/preview':previews++;json={played:true};break
  }
  await route.fulfill({json})
 })
 await page.goto(base!)
 const item=page.getByRole('link',{name:'歩行者用信号（高度化PICS）の設定'})
 await expect(item).toContainText('青まであと27秒')
 await expect(item).toContainText('目安')
 const itemBox=(await item.boundingBox())!
 const cameraBox=(await page.locator('.dashboard-camera-pair').boundingBox())!
 expect(itemBox.y).toBeGreaterThanOrEqual(cameraBox.y+cameraBox.height-1)
 signals=[{id:'00000001',slot:3,color:'red',seconds:null}]
 await expect(item).toContainText('赤・残り時間不明')
 await item.click()
 const panel=page.getByRole('region',{name:'高度化PICS（実験機能）'})
 const enable=panel.getByRole('checkbox',{name:'歩行者用信号をダッシュボードに表示する'})
 const voice=panel.getByRole('checkbox',{name:'VOICEVOXで本体から発話する'})
 await expect(enable).toBeChecked()
 await expect(voice).not.toBeChecked()
 await voice.check()
 await panel.getByRole('button',{name:'保存',exact:true}).click()
 await panel.getByRole('combobox',{name:'声',exact:true}).selectOption('3')
 await panel.getByRole('button',{name:'保存',exact:true}).click()
 await panel.getByRole('button',{name:'本体で試聴'}).click()
 await expect(panel.getByRole('status')).toHaveText('本体で再生しました')
 expect(previews).toBe(1)
 expect(settings).toMatchObject({speech:true,style:3,volume:60})
 await enable.uncheck()
 await panel.getByRole('button',{name:'保存',exact:true}).click()
 await expect(panel.getByRole('status')).toHaveText('保存しました')
 await page.goto(base!)
 await expect(page.locator('.dashboard-camera-pair')).toBeVisible()
 await expect(item).toHaveCount(0)
})
