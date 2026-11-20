export type PicsSettings={enabled:boolean;speech:boolean;directional?:boolean;style:number;volume:number}
export type PicsColor='red'|'green'|'blink-green'
export type PicsSignal={id:string;name?:string;slot:number;color:PicsColor;seconds:number|null;directionMatch?:boolean}
export type PicsStatus={settings:PicsSettings;state:'off'|'starting'|'scanning'|'unavailable';reason?:string;course?:number;signals:PicsSignal[]}
export type PicsSpeech={ready:boolean;prepared?:boolean;reason?:string;credit?:string;styles:{id:number;speaker:string;name:string}[]}
export type PicsPoint={lat:number;lon:number}
export type PicsReception={id:string;name?:string;slot:number;at:string;color:PicsColor;step?:number;position?:PicsPoint;beacon?:PicsPoint;course?:number;previousColor?:PicsColor;phaseSeconds?:number;cycleSeconds?:number;estimatedToGreen?:number}
export type PicsHistory={date:string;entries:PicsReception[];incomplete:boolean}
export type PicsAxes=(number|null)[]

export const picsColorText:Record<PicsColor,string>={red:'赤',green:'青','blink-green':'青点滅'}

export function picsCourseText(course?:number){
 if(course==null)return '方向不明'
 const labels=['北','北東','東','南東','南','南西','西','北西']
 return `${labels[Math.round(course/45)%8]} ${Math.round(course)%360}°`
}

export function isPicsStatus(value:PicsStatus|undefined):value is PicsStatus{
 return typeof value?.settings?.enabled==='boolean'&&Array.isArray(value.signals)
}

export function picsStatusText(status:PicsStatus){
 if(status.state==='off')return '無効'
 if(status.state==='unavailable')return status.reason||'BLE受信を利用できません'
 if(status.state==='starting')return 'BLEを確認中'
 return status.signals.length?'受信中':'受信待ち'
}

export function picsSignalText(signal:PicsSignal){
 if(signal.seconds!==null)return `青まであと${signal.seconds}秒`
 if(signal.color==='green')return '青'
 if(signal.color==='blink-green')return '青点滅'
 return '赤・残り時間不明'
}
