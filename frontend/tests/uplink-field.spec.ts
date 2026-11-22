import {expect,test} from '@playwright/test'

const ssid=process.env.NCC_FIELD_SSID
const secret=process.env.NCC_FIELD_PSK
const scenario=process.env.NCC_FIELD_CASE

test('NP1 connects to the exact hidden SSID entered in the UI',async({page})=>{
 test.skip(!ssid||!secret||scenario==='wrong-password','Requires the isolated USB AP and an ADB-forwarded trial NP1')
 test.setTimeout(130000)
 await page.goto('/#wifi')
 await page.getByRole('textbox',{name:'接続先SSID',exact:true}).fill(ssid!)
 await page.getByRole('textbox',{name:'Wi-Fi接続パスワード',exact:true}).fill(secret!)
 await page.getByRole('button',{name:'このWi-Fiへ接続',exact:true}).click()
 const confirm=page.getByRole('button',{name:'この接続を使う',exact:true})
 await expect(confirm).toBeEnabled({timeout:105000})
 await confirm.click()
 await expect(page.getByRole('status')).toContainText('接続を確定しました',{timeout:20000})
 await expect(page.locator('.wifi-current h3')).toHaveText(ssid!)
})

test('a wrong password cannot be confirmed and can be cancelled from the NP1 UI',async({page})=>{
 test.skip(!ssid||!secret||scenario!=='wrong-password','Requires the isolated USB AP wrong-password scenario')
 test.setTimeout(120000)
 await page.goto('/#wifi')
 await page.getByRole('textbox',{name:'接続先SSID',exact:true}).fill(ssid!)
 await page.getByRole('textbox',{name:'Wi-Fi接続パスワード',exact:true}).fill('wrong-'+secret!)
 await page.getByRole('button',{name:'このWi-Fiへ接続',exact:true}).click()
 const confirm=page.getByRole('button',{name:'この接続を使う',exact:true})
 await expect(confirm).toBeDisabled({timeout:50000})
 const cancel=page.getByRole('button',{name:'元に戻す',exact:true})
 await expect(cancel).toBeEnabled()
 await cancel.click()
 await expect(page.getByRole('status')).toContainText('元のWi-Fi接続へ戻しました',{timeout:25000})
})
