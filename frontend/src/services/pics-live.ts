import {computed, onMounted, onUnmounted, ref, shallowRef} from 'vue'
import {api} from './api'
import {isPicsStatus, type PicsStatus} from './pics'

export function useLivePICS(onStatus?: (status: PicsStatus) => void) {
 const latest = shallowRef<{status: PicsStatus; started: number}>()
 const now = ref(0)
 const connected = ref(false)
 const error = ref('')
 let active = false
 let clock: ReturnType<typeof setInterval> | undefined
 let nextPoll: ReturnType<typeof setTimeout> | undefined
 let request: AbortController | undefined
 let pending: Promise<void> | undefined

 const status = computed<PicsStatus | undefined>(() => {
  if (!latest.value) return
  const {status, started} = latest.value
  // The request start gives a conservative upper bound for snapshot age.
  const age = Math.max(0, now.value - started)
  const signals = connected.value ? status.signals.flatMap(signal => {
   if (age >= (signal.validForMs ?? 1250)) return []
   let seconds = signal.seconds
   if (signal.estimateMs !== undefined) {
    const estimate = age < (signal.estimateForMs ?? 0) ? signal.estimateMs - age : 0
    seconds = estimate > 0 ? Math.ceil(estimate / 1000) : null
   }
   return [{...signal, seconds}]
  }) : []
  return {...status, signals, stale: status.stale || (status.signals.length > 0 && !signals.length)}
 })

 function load() {
  if (pending) return pending
  const current = new AbortController()
  request = current
  const started = performance.now()
  const deadline = setTimeout(() => current.abort(), 1200)
  pending = (async () => {
   try {
    const value = await api<PicsStatus>('/pics', 'GET', undefined, true, current.signal)
    if (!isPicsStatus(value)) throw new Error('高度化PICSの状態を取得できません')
    if (!active || current.signal.aborted) return
    now.value = performance.now()
    latest.value = {status: value, started}
    connected.value = true
    error.value = ''
    onStatus?.(value)
   } catch (e) {
    if (active) {
     connected.value = false
     error.value = e instanceof Error ? e.message : String(e)
    }
   } finally {
    clearTimeout(deadline)
    request = undefined
    pending = undefined
   }
  })()
  return pending
 }

 async function refresh() {
  // A pending GET may predate the settings write that requested this refresh.
  await pending
  if (active) await load()
 }

 async function poll() {
  if (!active || document.hidden) return
  await load()
  if (active) {
   const interval = latest.value?.status.settings.enabled === false ? 2500 : 400
   nextPoll = setTimeout(poll, interval)
  }
 }

 function visibilityChanged() {
  if (document.hidden) return
  now.value = performance.now()
  if (!pending) {
   clearTimeout(nextPoll)
   void poll()
  }
 }

 onMounted(() => {
  active = true
  clock = setInterval(() => { now.value = performance.now() }, 80)
  document.addEventListener('visibilitychange', visibilityChanged)
  void poll()
 })
 onUnmounted(() => {
  active = false
  clearInterval(clock)
  clearTimeout(nextPoll)
  request?.abort()
  document.removeEventListener('visibilitychange', visibilityChanged)
 })
 return {status, connected, error, refresh}
}
