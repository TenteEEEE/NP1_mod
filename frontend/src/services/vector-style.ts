import type {LayerSpecification,StyleSpecification} from 'maplibre-gl'

export const vectorTileURL='https://tiles.openfreemap.org/planet'
export const mapAttribution='© <a href="https://www.openstreetmap.org/copyright" target="_blank" rel="noopener">OpenStreetMap</a> · <a href="https://openfreemap.org" target="_blank" rel="noopener">OpenFreeMap</a> · <a href="https://openmaptiles.org" target="_blank" rel="noopener">OpenMapTiles</a>'
const name=['coalesce',['get','name:ja'],['get','name'],['get','name:latin']]
const fonts=['Noto Sans Regular']
export type MapTheme='auto'|'mist'|'night'|'sage'|'colorful'
export type MapPalette=Exclude<MapTheme,'auto'>
export type MapView='dashboard'|'journey'|'playback'|'plates'
export const mapViews=[{id:'dashboard',name:'ダッシュボード'},{id:'journey',name:'走行履歴'},{id:'playback',name:'録画再生'},{id:'plates',name:'ナンバー記録'}] as const
export const mapThemes=[{id:'auto',name:'UIに合わせる'},{id:'mist',name:'ミスト · 青灰色'},{id:'night',name:'ナイト · 濃紺'},{id:'sage',name:'セージ · 緑灰色'},{id:'colorful',name:'カラー · 標準地図風'}] as const
export function mapPreferences(value?:Partial<Record<MapView,MapTheme>>):Record<MapView,MapTheme>{return {dashboard:value?.dashboard||'auto',journey:value?.journey||'auto',playback:value?.playback||'auto',plates:value?.plates||'auto'}}
export function vectorStyle(theme:MapPalette,background=true):StyleSpecification{
 const dark=theme==='night'
 const c=dark?{land:'#171d27',water:'#21374b',green:'#20372f',forest:'#263f34',sand:'#35342c',building:'#293345',edge:'#3b475a',road:'#566276',major:'#b2b9c8',highway:'#b6a78e',text:'#d5dfed',muted:'#94a3b8',halo:'#171d27',boundary:'#65758c',poi:'#98b7dc'}:{land:'#f3f5f9',water:'#d4e4f1',green:'#e4eee8',forest:'#d9e8df',sand:'#f0ebde',building:'#e0e5ee',edge:'#cbd3df',road:'#ffffff',major:'#ffffff',highway:'#f1dfbb',text:'#34465f',muted:'#71839a',halo:'#f8fafc',boundary:'#a3afc0',poi:'#617ea9'}
 if(theme==='sage')Object.assign(c,{land:'#f1f4ef',water:'#d8e5e3',green:'#e0e9d9',forest:'#d2e1ce',building:'#e0e5db',edge:'#c8d2c4',text:'#3e554d',muted:'#788b7e',poi:'#658777',halo:'#f6f8f3'})
 if(theme==='colorful')Object.assign(c,{land:'#f1f0e9',water:'#add5e8',green:'#cce4b6',forest:'#b5d5a6',sand:'#ede7c1',building:'#dedbd4',edge:'#c6c3bc',highway:'#edabb8',text:'#405159',muted:'#738071',poi:'#65759c',halo:'#fafaf3'})
 const layers:Record<string,unknown>[]=[{id:'surface',type:'background',paint:{'background-color':c.land}}]
 const add=(id:string,type:string,sourceLayer:string,minzoom:number,paint:Record<string,unknown>,layout?:Record<string,unknown>,filter?:unknown)=>layers.push({id,type,source:'basemap','source-layer':sourceLayer,minzoom,paint,...(layout?{layout}:{}),...(filter?{filter}:{})})
 if(background){
  add('landcover','fill','landcover',0,{'fill-color':['match',['get','class'],['wood','forest'],c.forest,'farmland',theme==='colorful'?'#e8e5c6':c.green,'grass',c.green,['sand','bare_rock'],c.sand,c.land],'fill-opacity':.75})
  if(theme==='colorful')add('landuse-areas','fill','landuse',9,{'fill-color':['match',['get','class'],'industrial','#e3d9e9',['commercial','retail'],'#f0dede','residential','#e8e7e3',c.land],'fill-opacity':.7})
  add('parks','fill','landuse',7,{'fill-color':c.green,'fill-opacity':.85},undefined,['in',['get','class'],['literal',['park','cemetery','recreation_ground','pitch','village_green']]])
  add('water','fill','water',0,{'fill-color':c.water})
  add('streams','line','waterway',10,{'line-color':c.water,'line-width':['interpolate',['linear'],['zoom'],10,.8,18,5]})
  add('boundaries','line','boundary',3,{'line-color':c.boundary,'line-opacity':.55,'line-width':['interpolate',['linear'],['zoom'],3,.5,12,1.2],'line-dasharray':[3,3]})
  add('buildings','fill','building',14,{'fill-color':c.building,'fill-outline-color':c.edge,'fill-opacity':['interpolate',['linear'],['zoom'],14,.4,16,.9]})
  const road=(id:string,classes:string[],min:number,width:number,color:string)=>{
   const filter=['in',['get','class'],['literal',classes]]
   add(id+'-edge','line','transportation',min,{'line-color':c.edge,'line-width':['interpolate',['exponential',1.5],['zoom'],min,.5,18,width+2],'line-opacity':.85},{'line-join':'round','line-cap':'round'},filter)
   add(id,'line','transportation',min,{'line-color':color,'line-width':['interpolate',['exponential',1.5],['zoom'],min,.35,18,width]},{'line-join':'round','line-cap':'round'},filter)
  }
  road('local-roads',['minor','service'],13,8,c.road)
  road('tertiary-roads',['tertiary'],11,10,c.road)
  road('secondary-roads',['secondary'],9,12,theme==='colorful'?'#f4d894':c.major)
  road('primary-roads',['primary','trunk'],7,14,theme==='colorful'?'#efbf88':c.major)
  road('motorways',['motorway'],4,16,c.highway)
  add('paths','line','transportation',15,{'line-color':c.muted,'line-opacity':.5,'line-width':['interpolate',['linear'],['zoom'],15,.6,19,2],'line-dasharray':[2,2]},undefined,['in',['get','class'],['literal',['path','track']]])
  add('railways','line','transportation',10,{'line-color':c.muted,'line-opacity':.65,'line-width':['interpolate',['linear'],['zoom'],10,.7,18,2],'line-dasharray':[3,2]},undefined,['in',['get','class'],['literal',['rail','transit']]])
  const textPaint={'text-color':c.text,'text-halo-color':c.halo,'text-halo-width':1.6,'text-halo-blur':.4}
  add('road-names','symbol','transportation_name',13,textPaint,{'symbol-placement':'line','symbol-spacing':350,'text-field':name,'text-font':fonts,'text-size':['interpolate',['linear'],['zoom'],13,10,18,13],'text-max-angle':30},['!',['in',['get','class'],['literal',['path','track','rail','transit']]]])
  add('major-places','symbol','place',3,textPaint,{'text-field':name,'text-font':fonts,'text-size':['interpolate',['linear'],['zoom'],3,11,9,15,15,18],'text-padding':18,'symbol-sort-key':['coalesce',['get','rank'],99]},['in',['get','class'],['literal',['country','state','city']]])
  add('towns','symbol','place',9,{...textPaint,'text-color':c.muted},{'text-field':name,'text-font':fonts,'text-size':['interpolate',['linear'],['zoom'],9,11,15,14],'text-padding':15,'symbol-sort-key':['coalesce',['get','rank'],99]},['==',['get','class'],'town'])
  add('neighbourhoods','symbol','place',13,{...textPaint,'text-color':c.muted},{'text-field':name,'text-font':fonts,'text-size':['interpolate',['linear'],['zoom'],13,10,18,13],'text-padding':12},['in',['get','class'],['literal',['village','suburb','quarter','neighbourhood','hamlet']]])
  add('poi-dots','circle','poi',15,{'circle-radius':['interpolate',['linear'],['zoom'],15,2,19,3],'circle-color':c.poi,'circle-stroke-color':c.halo,'circle-stroke-width':1})
  add('poi-names','symbol','poi',15,{...textPaint,'text-color':c.poi},{'text-field':name,'text-font':fonts,'text-size':['interpolate',['linear'],['zoom'],15,10,19,12],'text-anchor':'top','text-offset':[0,.7],'text-max-width':8,'text-padding':8,'symbol-sort-key':['coalesce',['get','rank'],99]},['step',['zoom'],['<=',['coalesce',['get','rank'],99],3],17,true])
  add('addresses','symbol','housenumber',17,{...textPaint,'text-color':c.muted,'text-halo-width':1},{'text-field':['to-string',['get','housenumber']],'text-font':fonts,'text-size':10,'text-padding':5})
 }
 return {version:8,name:'NCC '+theme,...(background?{glyphs:'https://tiles.openfreemap.org/fonts/{fontstack}/{range}.pbf'}:{}),sources:background?{basemap:{type:'vector',url:vectorTileURL,attribution:mapAttribution}}:{},layers:layers as LayerSpecification[]}
}
