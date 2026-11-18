import {test,expect} from '@playwright/test'

test.skip(process.env.NCC_JPEG_HARDWARE!=='1','Explicit live-camera JPEG field flag required')

test('front and cabin JPEG live view can use 1920x1080',async({page})=>{
 test.setTimeout(60000)
 await page.goto('/#camera')
 await page.getByRole('combobox',{name:'映像形式'}).selectOption('jpeg')
 await page.getByRole('combobox',{name:'JPEG解像度'}).selectOption('1080p')
 await page.getByRole('button',{name:'並べて表示',exact:true}).click()
 await page.getByRole('button',{name:'映像を表示',exact:true}).click()
 const images=page.locator('.live-camera-grid img')
 await expect(images).toHaveCount(2)
 await expect.poll(()=>images.evaluateAll(items=>items.every(item=>(item as HTMLImageElement).naturalWidth===1920&&(item as HTMLImageElement).naturalHeight===1080)),{timeout:30000}).toBe(true)
 await expect(page.getByText('1920 × 1080 · 最大 1 fps · JPEG',{exact:true})).toHaveCount(2)
 const first=await images.evaluateAll(items=>items.map(item=>(item as HTMLImageElement).src))
 await expect.poll(()=>images.evaluateAll((items,before)=>items.every((item,index)=>(item as HTMLImageElement).src!==before[index]),first),{timeout:10000}).toBe(true)
})
