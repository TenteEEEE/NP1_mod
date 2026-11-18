import {test,expect,type Page} from '@playwright/test'
const url=process.env.NCC_UI_REVIEW_URL
const group={id:'recorder',label:'ドライブレコーダー',items:[{id:'recorder-front',label:'前方カメラの録画品質',field:'field1',options:[{value:1,label:'高画質 1080p'},{value:2,label:'長時間 720p'}],writable:true}]}
const parking={id:'parking',label:'駐車中の録画',items:[{id:'parking-security',label:'駐車監視',field:'field1',options:[{value:1,label:'OFF'},{value:2,label:'ON'}],writable:true}]}
const vehicle={id:'vehicle',label:'車両・マイク',items:[{id:'vehicle-type',label:'車種',field:'field1',options:[{value:1,label:'セダン'},{value:2,label:'SUV'}],writable:true}]}
const volume={id:'volume',label:'本体の音量',items:[{id:'master-volume',label:'音量',field:'field1',options:Array.from({length:11},(_,value)=>({value,label:String(value)})),writable:true}]}
const account={id:'account',label:'アカウント',items:[{id:'login-state',label:'ログイン状態',field:'field1',options:[{value:1,label:'ログイン済み'},{value:2,label:'未ログイン'},{value:3,label:'起動時の認証更新中'}],writable:false}]}
const actions=[
 {id:'gnss-info',group:'GNSS・センサー',label:'GNSSの受信状態を取得',description:'受信状態を取得します。',readOnly:true,inputs:[]},
 {id:'account-login',group:'本体アカウントの操作',label:'本体にログイン',confirmation:'本体にログイン',description:'有効な認証情報が必要です。',readOnly:false,inputs:[{id:'refreshToken',label:'リフレッシュトークン',kind:'secret',max:1024},{id:'userKey',label:'ユーザーキー',kind:'secret',max:256}]},
 {id:'factory-reset',group:'セットアップ',label:'本体を初期化',confirmation:'本体を初期化',description:'設定が失われます。',readOnly:false,inputs:[]},
 {id:'sensor-trip',group:'GNSS・センサー',label:'センサートリップを取得',description:'トリップを読み取ります。',readOnly:true,unavailableReason:'FW7ではこの要求を処理しません。',inputs:[]},
 {id:'wifi-saved',group:'純正Wi-Fi接続',label:'純正の保存済みWi-Fi一覧を取得',description:'ページを指定します。',readOnly:true,inputs:[{id:'page',label:'ページ',kind:'number',min:1,max:100}]},
]
async function open(page:Page,failWrite=false){
 const writes:any[]=[],errors:string[]=[];let quality=1,processing=false
 page.on('pageerror',error=>errors.push(error.message))
 await page.addInitScript(()=>{(window as any).EventSource=class extends EventTarget{close(){}}})
 await page.route('**/api/v1/**',async route=>{
  const path=new URL(route.request().url()).pathname,method=route.request().method();let value:unknown={}
  if(method!=='GET')writes.push({path,body:route.request().postDataJSON()})
  if(path.endsWith('/auth/session'))value={csrf:'fixture'}
  else if(path.endsWith('/settings'))value={revision:1,theme:'system',locale:'ja',favorites:[]}
  else if(path.endsWith('/device/alexa'))value={status:'available',data:{wakeWord:{value:0,enabled:true,fixed:false},writable:true,linkage:{status:'not-fixed',source:'fixture'}}}
  else if(path.endsWith('/device/native-settings'))value={status:'available',data:{items:[]}}
  else if(path.endsWith('/device/native-audio'))value={status:'available',data:{masterVolume:18,mute:{value:1,enabled:false},correction:{value:2,label:'高'},writable:true}}
  else if(path.endsWith('/device/native-operations'))value={available:true,groups:[group,parking,vehicle,volume,account],actions}
  else if(path.endsWith('/device/recorder-advanced'))value={available:true,configured:true,hookLoaded:true,restartRequired:false,generation:1,applied:true,settings:{frontBitrateKbps:5000,cabinBitrateKbps:1000,frontRateControl:'vbr',cabinRateControl:'vbr',frontGopMilliseconds:1000,cabinGopMilliseconds:1000,cabinRecording:true,segmentDurationSeconds:180}}
  else if(path.endsWith('/actions/gnss-info'))value={sent:true,acknowledged:true,completionConfirmed:true,values:{satellites:12,usedSatellites:8},message:'本体の応答を確認しました。'}
  else if(path.endsWith('/actions/wifi-saved'))value={sent:true,acknowledged:true,completionConfirmed:true,values:{networks:[]},message:'一覧を取得しました。'}
  else if(path.endsWith('/actions/account-login'))return route.fulfill({status:503,json:{reason:'本体の応答を確認できません'}})
  else if(path.endsWith('/state/storage'))value={values:{inserted:1,formatStatus:processing?1:2},observedAt:new Date().toISOString()}
  else if(path.endsWith('/state/recorder'))value={values:{field1:quality},observedAt:new Date().toISOString()}
  else if(path.endsWith('/state/parking'))value={values:{field1:2},observedAt:new Date().toISOString()}
  else if(path.endsWith('/state/vehicle'))value={values:{field1:1},observedAt:new Date().toISOString()}
  else if(path.endsWith('/state/volume'))value={values:{field1:0},observedAt:new Date().toISOString()}
  else if(path.endsWith('/state/account'))value={values:{field1:2},observedAt:new Date().toISOString()}
  else if(path.endsWith('/sd-format/prepare'))value={token:'synthetic-confirmation',expiresAt:new Date(await page.evaluate(()=>Date.now())+60000).toISOString(),capacityBytes:64*1024**3}
  else if(path.endsWith('/sd-format/execute')){
   if(failWrite)return route.fulfill({status:503,json:{reason:'本体の応答を確認できません'}})
   processing=true;value={accepted:true,completionConfirmed:false,values:{inserted:1,formatStatus:1},message:'本体がフォーマット要求を受け付けました。'}
  }else if(path.endsWith('/settings/recorder-front')){quality=route.request().postDataJSON().value;value={values:{field1:quality},observedAt:new Date().toISOString(),readback:true}}
  return route.fulfill({json:value})
 })
 await page.goto(url!+'/#native')
 await expect(page.getByRole('button',{name:'SDカードをフォーマット',exact:true})).toBeEnabled()
 await expect(page.getByRole('button',{name:'本体の状態を更新',exact:true})).toBeEnabled()
  const accountState=page.getByLabel('ログイン状態：未ログイン')
  await expect(accountState).toHaveText('未ログイン')
  await expect(accountState).toHaveClass(/status-warning/)
 return {writes,errors}
}
test.describe('native app operations',()=>{
 test.skip(!url,'Use an isolated Vite UI; destructive requests are always mocked.')
 for(const width of [320,1440])test(`${width}px: app operations sit below Alexa and SD formatting requires explicit confirmation`,async({page},info)=>{
  await page.setViewportSize({width,height:width===320?740:1000});await page.emulateMedia({colorScheme:width===320?'dark':'light'})
  const state=await open(page)
  const alexa=await page.getByRole('heading',{name:'Alexa',exact:true}).boundingBox(),operations=await page.getByRole('heading',{name:'本体の基本設定',exact:true}).boundingBox()
  expect(operations!.y).toBeGreaterThan(alexa!.y)
  await page.getByRole('button',{name:'SDカードをフォーマット',exact:true}).click()
  const dialog=page.getByRole('dialog',{name:'SDカードをフォーマット'}),erase=dialog.getByRole('button',{name:'SDカードの全データを消去してフォーマット'})
  await expect(dialog).toContainText('64.0 GiB');await expect(erase).toBeDisabled()
  expect(state.writes.map(request=>request.path)).toEqual(['/api/v1/device/native-operations/sd-format/prepare'])
  await dialog.getByRole('checkbox').check();await expect(erase).toBeDisabled()
  await dialog.getByRole('textbox').fill('SDカードを消去');await expect(erase).toBeEnabled()
  await erase.scrollIntoViewIfNeeded();await expect(erase).toBeInViewport()
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true)
  await page.screenshot({path:info.outputPath('format-confirmation-'+width+'.png')})
  await erase.click();await expect(dialog).toHaveCount(0)
  await expect(page.getByRole('status')).toContainText('要求を受け付けました')
  await expect(page.getByRole('button',{name:'SDカードをフォーマット',exact:true})).toBeDisabled()
  expect(state.writes.at(-1).body).toEqual({token:'synthetic-confirmation',confirmation:'SDカードを消去',acknowledgeDataLoss:true})
  expect(state.errors).toEqual([])
 })
 test('cancel does not format, settings use expected values and readback',async({page})=>{
  const state=await open(page)
  await page.getByRole('button',{name:'SDカードをフォーマット',exact:true}).click();await page.getByRole('dialog').getByRole('button',{name:'キャンセル'}).click()
  expect(state.writes.some(request=>request.path.endsWith('/execute'))).toBe(false)
  await page.goto(url!+'/#system/recorder')
  const setting=page.getByRole('combobox',{name:'前方カメラの録画品質'})
  await setting.selectOption('2');await page.getByRole('region',{name:'ドライブレコーダーの本体操作'}).getByRole('button',{name:'変更を適用',exact:true}).click()
  await expect(page.getByRole('status')).toContainText('本体から再取得しました')
  expect(state.writes.at(-1).body).toEqual({expectedValue:1,value:2})
  await expect(setting).toHaveValue('2')
 })
 test('unconfirmed destructive response is never retried or labelled completed',async({page})=>{
  const state=await open(page,true);await page.getByRole('button',{name:'SDカードをフォーマット',exact:true}).click()
  const dialog=page.getByRole('dialog');await dialog.getByRole('checkbox').check();await dialog.getByRole('textbox').fill('SDカードを消去');await dialog.getByRole('button',{name:'SDカードの全データを消去してフォーマット'}).click()
  await expect(page.getByRole('alert')).toContainText('操作を自動で再送しません')
  await expect(page.getByRole('status')).toHaveCount(0)
  expect(state.writes.filter(request=>request.path.endsWith('/execute'))).toHaveLength(1)
  await expect(page.getByRole('button',{name:'SDカードをフォーマット',exact:true})).toBeDisabled()
 })
 test('expired confirmation disables execution without submitting a request',async({page})=>{
  await page.clock.install({time:new Date('2026-11-16T00:00:00Z')})
  const state=await open(page);await page.getByRole('button',{name:'SDカードをフォーマット',exact:true}).click()
  const dialog=page.getByRole('dialog');await dialog.getByRole('checkbox').check();await dialog.getByRole('textbox').fill('SDカードを消去')
  const erase=dialog.getByRole('button',{name:'SDカードの全データを消去してフォーマット'});await expect(erase).toBeEnabled()
  await page.clock.fastForward(61000);await expect(erase).toBeDisabled()
  expect(state.writes.some(request=>request.path.endsWith('/execute'))).toBe(false)
 })
 for(const width of [320,1440])test(`${width}px: operation cards form a responsive grid and never send writes on load`,async({page},info)=>{
  await page.setViewportSize({width,height:950});const state=await open(page)
  await page.getByRole('button',{name:/操作・診断/}).click()
  await page.getByRole('button',{name:/接続・アカウント/}).click()
  const grid=page.locator('.native-action-grid'),columns=await grid.evaluate(el=>getComputedStyle(el).gridTemplateColumns.split(' ').length)
  expect(width===320?columns:Math.min(columns,2)).toBe(width===320?1:2)
  expect(state.writes).toEqual([])
  await page.getByRole('button',{name:/調整・診断/}).click()
  await page.getByRole('button',{name:'GNSSの受信状態を取得',exact:true}).click()
  await expect(page.getByRole('region',{name:'GNSS・センサーの操作'})).toContainText('12')
  await expect(page.getByRole('button',{name:'センサートリップを取得',exact:true})).toBeDisabled()
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true)
  await grid.screenshot({path:info.outputPath('operation-grid-'+width+'.png')})
 })
 test('recorder and audio controls appear only in their system tabs',async({page})=>{
  await open(page)
  for(const name of ['ドライブレコーダー','駐車中の録画','H.264詳細録画設定','本体の音量','本体の音声設定'])await expect(page.getByRole('heading',{name,exact:true})).toHaveCount(0)
  await page.goto(url!+'/#system/recorder')
  for(const name of ['ドライブレコーダー','駐車中の録画','H.264詳細録画設定'])await expect(page.getByRole('heading',{name,exact:true})).toBeVisible()
  await page.getByRole('button',{name:'LED / 音声',exact:true}).click()
  for(const name of ['本体の音量','消音・音量補正'])await expect(page.getByRole('heading',{name,exact:true})).toBeVisible()
  await expect(page.getByTestId('native-master-volume')).toHaveCount(0)
  await expect(page.getByRole('heading',{name:'H.264詳細録画設定',exact:true})).toHaveCount(0)
 })
 test('credentials use a confirmed POST, failure is not retried, and factory reset requires a typed phrase',async({page})=>{
  const state=await open(page);await page.getByRole('button',{name:/操作・診断/}).click();await page.getByRole('button',{name:/接続・アカウント/}).click();await page.getByRole('button',{name:'本体にログイン',exact:true}).click();const dialog=page.getByRole('dialog')
  await dialog.getByLabel('リフレッシュトークン').fill('synthetic-only');await dialog.getByLabel('ユーザーキー').fill('fixture-only')
  await expect(dialog.getByRole('button',{name:'本体にログイン',exact:true})).toBeDisabled();await dialog.getByRole('checkbox').check();await dialog.getByRole('button',{name:'本体にログイン',exact:true}).click()
  await expect(page.getByRole('region',{name:'本体アカウントの操作の操作'}).getByRole('alert')).toContainText('自動で再送しません')
  expect(state.writes.filter(w=>w.path.endsWith('/account-login'))).toHaveLength(1)
  await page.getByRole('button',{name:/保守・セットアップ/}).click();await page.getByRole('button',{name:'本体を初期化',exact:true}).click();await dialog.getByRole('checkbox').check();await expect(dialog.getByRole('button',{name:'本体を初期化',exact:true})).toBeDisabled()
  await dialog.getByRole('textbox').fill('本体を初期化');await expect(dialog.getByRole('button',{name:'本体を初期化',exact:true})).toBeEnabled();await dialog.getByRole('button',{name:'キャンセル'}).click()
  expect(state.writes.some(w=>w.path.endsWith('/factory-reset'))).toBe(false)
 })
 test('parameterized reads accept a page without a mutation confirmation',async({page})=>{
  const state=await open(page);await page.getByRole('button',{name:/操作・診断/}).click();await page.getByRole('button',{name:/接続・アカウント/}).click();await page.getByRole('button',{name:'純正の保存済みWi-Fi一覧を取得',exact:true}).click();const dialog=page.getByRole('dialog')
  await expect(dialog.getByRole('checkbox')).toHaveCount(0);await dialog.getByLabel('ページ').fill('2');await dialog.getByRole('button',{name:'純正の保存済みWi-Fi一覧を取得',exact:true}).click()
  await expect(page.getByRole('region',{name:'純正Wi-Fi接続の操作'}).getByRole('status')).toContainText('一覧を取得しました。')
  expect(state.writes.at(-1).body).toEqual({values:{page:2}})
 })
})
