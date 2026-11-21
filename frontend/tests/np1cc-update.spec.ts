import { expect, test } from '@playwright/test'

const reviewURL = process.env.NCC_UI_REVIEW_URL

test('NP1CC update options and immediate apply are separate from stock OTA controls', async ({ page }) => {
  test.skip(!reviewURL, 'Set NCC_UI_REVIEW_URL to an isolated static UI server.')
  const saved = { current: '0.5.0', settings: { mode: 'check', allow4G: false } }
  const writes: { path: string; method: string; body: unknown }[] = []
  await page.route('**/api/v1/**', async route => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    if (path.includes('/np1cc-update')) {
      if (request.method() !== 'GET') writes.push({ path, method: request.method(), body: request.postDataJSON() })
      if (request.method() === 'PUT') saved.settings = request.postDataJSON()
      if (path.endsWith('/apply')) return route.fulfill({ status: 202, json: { ...saved, phase: 'applying' } })
      if (path.endsWith('/check')) return route.fulfill({ status: 202, json: { ...saved, latest: { version: '0.5.1' } } })
      return route.fulfill({ json: saved })
    }
    if (path.endsWith('/events')) return route.fulfill({ contentType: 'text/event-stream', body: ': fixture\n\n' })
    if (path.endsWith('/auth/session')) return route.fulfill({ json: { csrf: 'test-fixture' } })
    if (path.endsWith('/settings')) return route.fulfill({ json: { revision: 1, theme: 'system', locale: 'ja', favorites: [] } })
    if (path.endsWith('/capabilities')) return route.fulfill({ json: [] })
    return route.fulfill({ json: {} })
  })

  await page.goto(`${reviewURL}/#system/settings`)
  await expect(page.getByRole('heading', { name: '純正ソフトウェアの更新抑止', exact: true })).toBeVisible()
  const update = page.getByRole('region', { name: 'NP1 Control Center の更新' })
  const mode = update.getByLabel('自動更新モード')
  const transport = update.getByLabel('更新に使う回線')
  await expect(mode.locator('option')).toHaveCount(4)
  await expect(transport.locator('option')).toHaveCount(2)
  await expect(mode).toHaveValue('check')
  await mode.selectOption('next-boot')
  await transport.selectOption({ label: 'Wi-Fi と 4G を許可' })
  await update.getByRole('button', { name: '更新設定を保存' }).click()
  await expect.poll(() => writes[0]?.body).toEqual({ mode: 'next-boot', allow4G: true })
  await update.getByRole('button', { name: '更新を確認' }).click()
  await expect(update.getByText('候補 v0.5.1')).toBeVisible()
  await update.getByRole('button', { name: '即時更新を実行' }).click()
  await update.getByRole('button', { name: 'キャンセル' }).click()
  expect(writes).toHaveLength(2)
  await update.getByRole('button', { name: '即時更新を実行' }).click()
  await update.getByRole('button', { name: '更新を開始' }).click()
  await expect(update.getByRole('status')).toHaveText('更新を適用中')
  expect(writes.map(entry => entry.path)).toEqual([
    '/api/v1/system/np1cc-update', '/api/v1/system/np1cc-update/check', '/api/v1/system/np1cc-update/apply',
  ])
})
