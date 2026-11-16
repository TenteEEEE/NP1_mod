import {api,type Observation} from './api'

import type {TrackPoint} from './playbackTrack'
type Track={points:TrackPoint[]}

// recentTrail returns the recorded driving track of the last `hours`, split into line segments
// wherever the recorder's segment changes or fixes are more than five seconds apart.
export async function recentTrail(hours=24,now=Date.now()):Promise<[number,number][][]>{
 const since=now-hours*3600000
 const query=new URLSearchParams({from:new Date(since).toISOString(),to:new Date(now).toISOString()})
 const value=await api<Observation<Track>>('/recordings/track?'+query)
 if(!Array.isArray(value.data?.points))throw new Error('走行経路を取得できません')
 const sorted=value.data.points.filter(p=>Number.isFinite(p.lat)&&Math.abs(p.lat)<=90&&Number.isFinite(p.lon)&&Math.abs(p.lon)<=180&&Date.parse(p.at)>=since&&Date.parse(p.at)<=now).sort((a,b)=>Date.parse(a.at)-Date.parse(b.at))
 const segments:[number,number][][]=[]
 let last:TrackPoint|undefined
 for(const p of sorted){
  if(!last||p.segment!==last.segment||Date.parse(p.at)-Date.parse(last.at)>5000)segments.push([])
  segments[segments.length-1]!.push([p.lat,p.lon])
  last=p
 }
 return segments.filter(s=>s.length)
}
