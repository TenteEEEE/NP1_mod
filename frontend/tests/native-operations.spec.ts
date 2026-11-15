import {test,expect,type Page} from '@playwright/test'
const url=process.env.NCC_UI_REVIEW_URL
const group={id:'recorder',label:'ドライブレコーダー',items:[{id:'recorder-front',label:'前方カメラの録画品質',field:'field1',options:[{value:1,label:'高画質 1080p'},{value:2,label:'長時間 720p'}],writable:true}]}
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
  else if(path.endsWith('/device/native-operations'))value={available:true,groups:[group]}
  else if(path.endsWith('/state/storage'))value={values:{inserted:1,formatStatus:processing?1:2},observedAt:new Date().toISOString()}
  else if(path.endsWith('/state/recorder'))value={values:{field1:quality},observedAt:new Date().toISOString()}
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
 return {writes,errors}
}
test.describe('native app operations',()=>{
 test.skip(!url,'Use an isolated Vite UI; destructive requests are always mocked.')
 for(const width of [320,1440])test(`${width}px: app operations sit below Alexa and SD formatting requires explicit confirmation`,async({page},info)=>{
  await page.setViewportSize({width,height:width===320?740:1000});await page.emulateMedia({colorScheme:width===320?'dark':'light'})
  const state=await open(page)
  const alexa=await page.getByRole('heading',{name:'Alexa',exact:true}).boundingBox(),operations=await page.getByRole('heading',{name:'本体の操作',exact:true}).boundingBox()
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
  const setting=page.getByRole('combobox',{name:'前方カメラの録画品質'})
  await setting.selectOption('2');await page.getByRole('button',{name:'変更を適用',exact:true}).click()
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
})
