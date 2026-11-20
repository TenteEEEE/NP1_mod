export type PicsSettings={enabled:boolean;speech:boolean;style:number;volume:number}
export type PicsSignal={id:string;name?:string;slot:number;color:'red'|'green'|'blink-green';seconds:number|null}
export type PicsStatus={settings:PicsSettings;state:'off'|'starting'|'scanning'|'unavailable';reason?:string;signals:PicsSignal[]}
export type PicsSpeech={ready:boolean;reason?:string;credit?:string;styles:{id:number;speaker:string;name:string}[]}

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
