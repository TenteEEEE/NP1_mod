import {expect,test} from '@playwright/test'
import type {PicsAxes,PicsSettings,PicsSignal} from '../src/services/pics'
import {clickVectorFeature,vectorState} from './fixtures/vector-tiles'

const base=process.env.NCC_UI_REVIEW_URL
test.skip(!base,'Local Vite fixtures required')

for(const width of [1440,390])test(`高度化PICS専用画面と受信記録 (${width}px)`,async({page})=>{
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
   case '/api/v1/pics/history': {
    const date=new URL(route.request().url()).searchParams.get('date')
    const entries=date==='2026-11-20'?[]:[
     {id:'00000002',name:'測位なし交差点',slot:1,color:'red',at:'2026-11-21T04:00:00Z',step:8},
     ...Array.from({length:5},(_,i)=>({id:'00000001',name:'試験交差点',slot:3,color:i===4?'blink-green':'green',at:`2026-11-21T04:0${i}:00Z`,position:{lat:20+i*.0001,lon:100},beacon:{lat:20,lon:100.0002},course:90,previousColor:'red',phaseSeconds:24,cycleSeconds:59+i*.4,step:4,estimatedToGreen:i===3?27:undefined})),
    ]
    json={date,entries,incomplete:false}
    break
   }
   case '/api/v1/pics/directions':json={'00000001':axes};break
   case '/api/v1/pics/directions/00000001':axes=route.request().postDataJSON().axes;json=axes;break
  }
  await route.fulfill({json})
 })
 await page.goto(base!)
 const item=page.getByRole('link',{name:'高度化PICSを開く'})
 await expect(item).toContainText('青まであと27秒')
 await expect(item).toContainText('目安')
 const itemBox=(await item.boundingBox())!
 const cameraBox=(await page.locator('.dashboard-camera-pair').boundingBox())!
 expect(itemBox.y).toBeGreaterThanOrEqual(cameraBox.y+cameraBox.height-1)
 signals=[{id:'00000001',slot:3,color:'red',seconds:null}]
 await expect(item).toContainText('赤・残り時間不明')
 await item.click()
 await expect(page).toHaveURL(/#system\/pics$/)
 const system=page.getByRole('navigation',{name:'システム設定'})
 const picsTab=system.getByRole('button',{name:'高度化PICS',exact:true})
 await expect(picsTab).toHaveAttribute('aria-current','page')
 await expect(picsTab).toBeInViewport({ratio:1})
 await expect(page.getByRole('heading',{name:'高度化PICS（実験機能）'})).toBeVisible()
 const records=page.getByRole('tabpanel',{name:'受信記録'})
 const mapSelector='.plate-map[aria-label="高度化PICSの受信地点地図"]'
 const map=records.getByRole('region',{name:'高度化PICSの受信地点地図'})
 const list=records.getByRole('region',{name:'受信記録一覧'})
 await expect(map).toHaveAttribute('data-features-pics-points','6')
 await expect(list.getByRole('button',{name:/青点滅.*試験交差点/})).toBeVisible()
 await expect(list.getByRole('button',{name:/測位なし交差点.*受信地点なし/})).toBeVisible()
 const mapBox=(await map.boundingBox())!,listBox=(await list.boundingBox())!
 if(width===1440)expect(listBox.x).toBeGreaterThanOrEqual(mapBox.x+mapBox.width)
 else expect(listBox.y).toBeGreaterThanOrEqual(mapBox.y+mapBox.height)
 const card=list.getByRole('button',{name:/青まで約27秒/})
 await card.click()
 await expect(card).toHaveAttribute('aria-pressed','true')
 await expect(map).toHaveAttribute('data-map-center','100.0000000,20.0003000')
 await records.getByRole('checkbox',{name:'地図の背景を表示'}).uncheck()
 await expect(map).toHaveAttribute('data-map-background','off')
 await expect(map).toHaveAttribute('data-features-pics-points','6')
 await expect.poll(async()=>Boolean((await vectorState(page,mapSelector))?.rendered.some((point:{source:string;properties:{index:number}})=>point.source==='pics-points'&&point.properties.index===4))).toBe(true)
 await clickVectorFeature(page,mapSelector,'pics-points',{key:'index',value:4})
 await expect(list.locator('[data-entry="4"]')).toHaveAttribute('aria-pressed','true')
 await records.getByText('灯器ごとの観測周期と予測').click()
 await expect(records.getByText('約59.8秒',{exact:true})).toBeVisible()
 const day=records.getByLabel('受信日（日本時間）')
 await day.fill('2026-11-20')
 await expect(list.getByText('この日の受信記録はありません。')).toBeVisible()
 await day.fill('2026-11-21')
 const crossing=records.getByRole('combobox',{name:'交差点',exact:true})
 await crossing.selectOption('00000001')
 await expect(list.getByRole('button',{name:/測位なし交差点/})).toHaveCount(0)
 await records.getByText('この交差点の横断方向を登録').click()
 await records.getByRole('spinbutton',{name:'信号3の方位'}).fill('90')
 await records.getByRole('button',{name:'横断方向を保存'}).click()
 await expect(records.getByText('横断方向を保存しました')).toBeVisible()
 expect(axes[2]).toBe(90)

 await page.getByRole('tab',{name:'受信・発話設定'}).click()
 const panel=page.getByRole('tabpanel',{name:'受信・発話設定'})
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
 await page.getByRole('tab',{name:'受信・発話設定'}).press('ArrowLeft')
 await expect(page.getByRole('tab',{name:'受信記録'})).toBeFocused()
 await expect(crossing).toHaveValue('00000001')
 await page.getByRole('tab',{name:'受信・発話設定'}).click()
 await enable.uncheck()
 await panel.getByRole('button',{name:'保存',exact:true}).click()
 await expect(panel.getByRole('status')).toHaveText('保存しました')
 await system.getByRole('button',{name:'設定・バックアップ',exact:true}).click()
 await expect(page.getByRole('heading',{name:'表示設定',exact:true})).toBeVisible()
 await expect(page.getByRole('heading',{name:'高度化PICS（実験機能）'})).toHaveCount(0)
 await system.getByRole('button',{name:'高度化PICS',exact:true}).click()
 await expect(records).toBeVisible()
 await page.goto(base!)
 await expect(page.locator('.dashboard-camera-pair')).toBeVisible()
 await expect(item).toHaveCount(0)
})
