import type {Page} from '@playwright/test'
import {fromGeojsonVt} from '@maplibre/vt-pbf'
import {existsSync,readFileSync} from 'node:fs'

const point=(x:number,y:number,tags:Record<string,unknown>)=>({type:1,geometry:[[x,y]],tags})
const line=(geometry:number[][],tags:Record<string,unknown>)=>({type:2,geometry:[geometry],tags})
const polygon=(geometry:number[][],tags:Record<string,unknown>)=>({type:3,geometry:[geometry],tags})
const pbf=Buffer.from(fromGeojsonVt(Object.fromEntries(Object.entries({
 landcover:[polygon([[0,0],[0,1400],[1400,1400],[1400,0],[0,0]],{class:'wood'})],
 water:[polygon([[3400,0],[3400,4096],[4096,4096],[4096,0],[3400,0]],{class:'river'})],
 transportation:[line([[0,2048],[4096,2048]],{class:'primary'}),line([[1800,0],[1800,4096]],{class:'minor'})],
 transportation_name:[line([[0,2048],[4096,2048]],{class:'primary',name:'テスト通り'})],
 place:[point(600,600,{class:'city',name:'テスト市',rank:1}),point(2600,1000,{class:'neighbourhood',name:'試験町',rank:5})],
 building:[polygon([[2200,2300],[2200,2900],[2700,2900],[2700,2300],[2200,2300]],{render_height:20})],
 poi:[point(2400,2100,{class:'shop',name:'テスト施設',rank:1})],
 housenumber:[point(2450,2600,{housenumber:'12'})]
}).map(([key,features])=>[key,{features}])) as any,{version:2,extent:4096}))
export async function vectorTiles(page:Page,fail=false){
 const requests:string[]=[]
 await page.route('https://tiles.openfreemap.org/**',async route=>{
  requests.push(route.request().url())
  if(fail)return route.abort()
  const url=new URL(route.request().url())
  if(url.pathname==='/planet')return route.fulfill({json:{tilejson:'3.0.0',tiles:['https://tiles.openfreemap.org/test/{z}/{x}/{y}.pbf'],minzoom:0,maxzoom:14,attribution:'Synthetic vector tiles'}})
  if(url.pathname.startsWith('/fonts/')){const path='../tmp/vector-ascii-font.pbf';return route.fulfill({body:existsSync(path)?readFileSync(path):Buffer.alloc(0),contentType:'application/x-protobuf'})}
  return route.fulfill({body:pbf,contentType:'application/x-protobuf'})
 })
 return requests
}
export async function vectorState(page:Page,selector:string){
 return page.locator(selector).evaluate(async element=>{
  const {inspectVectorMap}=await import('/src/services/vector-map.ts' as string)
  const map=inspectVectorMap(element as HTMLElement)
  if(!map||!map.getStyle())return null
  const style=map.getStyle(),sources:Record<string,any>={}
  for(const [key,source]of Object.entries(style.sources)){if((source as any).type==='geojson')sources[key]=(source as any).data}
  return {center:map.getCenter().toArray(),zoom:map.getZoom(),moving:map.isMoving(),sources,layers:style.layers.map((layer:any)=>({id:layer.id,type:layer.type,minzoom:layer.minzoom,paint:layer.paint})),rendered:map.queryRenderedFeatures().map((f:any)=>({layer:f.layer.id,source:f.source,properties:f.properties})),styleName:style.name}
 })
}
export async function overlayCount(page:Page,selector:string,source:string){return (await vectorState(page,selector))?.sources[source]?.features?.length??0}
export async function clickVectorFeature(page:Page,selector:string,source:string,property?:{key:string;value:unknown}){
 const point=await page.locator(selector).evaluate(async(element,args)=>{
  const {inspectVectorMap}=await import('/src/services/vector-map.ts' as string),map=inspectVectorMap(element as HTMLElement)!
  const data=(map.getStyle().sources[args.source] as any)?.data
  const feature=data.features.find((f:any)=>!args.property||f.properties[args.property.key]===args.property.value)
  let coordinate=feature.geometry.coordinates
  if(feature.geometry.type==='LineString')coordinate=coordinate[Math.floor(coordinate.length/2)]
  if(feature.geometry.type==='Polygon')coordinate=coordinate[0][0]
  let p=map.project(coordinate)
  if(p.x<10||p.y<10||p.x>element.clientWidth-10||p.y>element.clientHeight-10){map.jumpTo({center:coordinate});p=map.project(coordinate)}
  return {x:p.x,y:p.y}
 },{source,property})
 await page.locator(selector).click({position:point})
}
