<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { RefreshCw } from '@lucide/vue'
import { api, bytes, date } from '../services/api'

type Mode = 'immediate' | 'next-boot' | 'check' | 'off'
type Settings = { mode: Mode; allow4G: boolean }
type Status = {
  settings: Settings
  current: string
  latest?: { version: string; notes?: string; size?: number; reason?: string }
  pendingVersion?: string
  lastChecked?: string
  phase?: 'checking' | 'downloading' | 'applying'
  result?: string
  message?: string
}

const endpoint = '/system/np1cc-update'
const labels = { checking: '更新を確認中', downloading: 'ダウンロード中', applying: '更新を適用中' }
const status = ref<Status>()
const draft = ref<Settings>({ mode: 'check', allow4G: false })
const error = ref('')
const changing = ref(false)
const confirming = ref(false)
const waitingForWeb = ref(false)
const dirty = computed(() => !!status.value && (draft.value.mode !== status.value.settings.mode || draft.value.allow4G !== status.value.settings.allow4G))
const busy = computed(() => changing.value || !!status.value?.phase)
let polling: ReturnType<typeof setInterval> | undefined
let reading = false
let requestGeneration = 0

function readStatus(value: Status, resetDraft = false) {
  if (!value.settings || typeof value.current !== 'string') throw new Error('更新サービスの応答を確認できません')
  if (resetDraft || !dirty.value) draft.value = { ...value.settings }
  status.value = value
  waitingForWeb.value = false
}

async function load() {
  if (reading || changing.value) return
  reading = true
  const generation = requestGeneration
  try {
    const value = await api<Status>(endpoint)
    if (generation === requestGeneration) readStatus(value)
  } catch {
    if (generation === requestGeneration && status.value?.phase) waitingForWeb.value = true
  } finally {
    reading = false
  }
}

async function submit(action: 'save' | 'check' | 'apply') {
  if (busy.value) return
  confirming.value = false
  changing.value = true
  requestGeneration++
  error.value = ''
  try {
    const value = action === 'save'
      ? await api<Status>(endpoint, 'PUT', draft.value)
      : await api<Status>(`${endpoint}/${action}`, 'POST', {})
    readStatus(value, action === 'save')
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '操作に失敗しました'
  } finally {
    changing.value = false
  }
}

onMounted(() => {
  void load()
  polling = setInterval(load, 3000)
})
onUnmounted(() => clearInterval(polling))
</script>

<template>
  <section class="panel" aria-labelledby="np1cc-update-title">
    <div class="panel-head"><h2 id="np1cc-update-title"><RefreshCw :size="18"/>NP1 Control Center の更新</h2><span v-if="status" class="tag">v{{ status.current }}</span></div>
    <div class="settings-body">
      <template v-if="status">
        <label>自動更新モード
          <select v-model="draft.mode" :disabled="busy">
            <option value="immediate">新バージョンがあれば即時更新</option>
            <option value="next-boot">ダウンロードし、次回起動時に更新</option>
            <option value="check">更新確認のみ</option>
            <option value="off">更新しない</option>
          </select>
        </label>
        <label>更新に使う回線
          <select v-model="draft.allow4G" :disabled="busy">
            <option :value="false">Wi-Fi のみ</option>
            <option :value="true">Wi-Fi と 4G を許可</option>
          </select>
        </label>
        <button class="secondary" :disabled="!dirty || busy" @click="submit('save')">更新設定を保存</button>
        <p class="muted">最終確認 {{ date(status.lastChecked) }}</p>
        <p v-if="status.latest">候補 v{{ status.latest.version }}<template v-if="status.latest.size"> · {{ bytes(status.latest.size) }}</template></p>
        <p v-if="status.latest?.reason">{{ status.latest.reason }}</p>
        <details v-if="status.latest?.notes"><summary>変更内容</summary><p class="update-notes">{{ status.latest.notes }}</p></details>
        <p v-if="status.pendingVersion">v{{ status.pendingVersion }} をダウンロード済みです。</p>
        <p v-if="waitingForWeb || status.phase || status.message" role="status">{{ waitingForWeb ? '管理画面の再接続を待っています' : status.phase ? labels[status.phase] : status.message }}</p>
        <div class="update-actions">
          <button class="secondary" :disabled="busy || dirty" @click="submit('check')">更新を確認</button>
          <button class="primary" :disabled="busy || dirty" @click="confirming = true">即時更新を実行</button>
        </div>
        <div v-if="confirming" class="update-actions">
          <p>更新中は管理画面とライブ表示が一時切断します。本体の電源を維持してください。</p>
          <button class="secondary" @click="confirming = false">キャンセル</button>
          <button class="primary" :disabled="busy" @click="submit('apply')">更新を開始</button>
        </div>
      </template>
      <p v-else role="status">NP1CC 更新サービスの接続を確認しています。未導入の場合は初回設定が必要です。</p>
      <p v-if="error" role="alert">{{ error }}</p>
    </div>
  </section>
</template>

<style scoped>
.update-actions { display: flex; align-items: center; flex-wrap: wrap; gap: .5rem; }
.update-actions p { flex-basis: 100%; }
.update-notes { white-space: pre-wrap; overflow-wrap: anywhere; }
</style>
