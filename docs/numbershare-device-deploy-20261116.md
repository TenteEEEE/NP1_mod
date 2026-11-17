# NumberShare実機導入記録（2026-11-16）

## 対象と方法

- ブランチ `feat/number-share-api`、実装コミット `c3cfe53` を基に標準ビルドを再実行した。
- Windows側のWi-Fi接続先・プロファイル・設定は変更していない。既存のroot ADB接続だけを使用した。
- 変更対象はWeb/APIバイナリと内包UIのため、`python tools/deploy.py --serial <明示指定> --action web-worker-update` を使用した。
- 実機変更は `tools/device/web-worker-update.sh` が実行し、旧バイナリ、更新前後のAP・モデム・native worker状態を復旧領域へ保存した。失敗時は旧バイナリへ自動復元する手順である。
- 復旧スナップショットは `/overlay/owner-apps/np1-control/recovery/web-worker-20261116T064813Z`。

## read-back

- 配備成果物と実機 `/overlay/owner-apps/np1-control/bin/ncc` のSHA-256が一致した。
- `np1-control.service` と `np1-control-broker.service` は新PIDでactive、`NRestarts=0`、`Result=success`。
- `np1-control-ap.service`、`np1-control-modem.service`、`np1-control-native.service` のPIDは更新前後で同一だった。
- `app-@Communication`、`app-@DriveRec`、`app-@ImageRecog`、`app-@Voice`、`qmmf-server` は更新後もactive/runningだった。
- 新しい `GET /api/v1/plate/share/status` は200を返し、初期状態はrevision 1、送信無効、エンドポイント・キー未設定、キュー0件だった。秘密値は応答に含まれない。

## 検証

- `tools/connect.py` で既存ADB接続へHTTP 80 / HTTPS 443を転送し、証明書指紋をADB経由で再確認した。
- `tools/hardware_verify.py --camera` は、Webパスワードなしのセッション、純正サービス、SD上MP4のRange取得、パストラバーサル拒否、前方／車内JPEG、バックアップの秘密情報除外をすべてPASSとした。
- 配備スクリプト内の `tools/device/verify.sh` はHTTP/HTTPS、UI shell、非root Web worker、plate sensor socketを確認して成功した。
- 全Playwrightは60件成功、203件スキップ、11件失敗。11件はすべて、Vite開発サーバ専用の `/src/services/vector-map.ts` を本番配備UIから動的importするテスト補助コードの実行先不一致であり、同じ原因だった。実機基本UIテストとNumberShareの合成E2Eは成功した。
- NumberShare実機専用GET-onlyテストで、共有タブ、保存キーの既定伏せ字と表示切替、初期キュー、モバイル幅の非オーバーフローを確認し、1件成功した。

本番NumberShare APIのエンドポイントと認証キーは未設定のままにした。外部APIへの検知送信や共有データ取得は行っていない。

## 全件受信・期間アップロード追加導入

- API履歴を固定500件で打ち切らず、カーソル終端までGETで取得する全件受信へ更新した。
- JSTの開始日・終了日を指定し、期間内の全ローカル検知を重複なく永続キューへ追加する手動アップロードを導入した。手動項目はリアルタイム送信の停止中でも処理する。
- 全体ビルド、Go全パッケージのtest/vet、postboot 139件、NumberShare合成E2E、公開ツリーと既知の秘密値の漏えい検査に合格した。
- `web-worker-update` でWeb/APIだけを更新し、復旧スナップショット `/overlay/owner-apps/np1-control/recovery/web-worker-20261116T070737Z` を作成した。配備物と実機バイナリのSHA-256一致をread-backした。
- 更新後もWeb/API、broker、ナンバー認識と純正Communication／DriveRec／ImageRecog／Voice／QMMFはactive/running、`NRestarts=0`。API接続先と認証キーは未設定のため、外部送信は発生していない。

## 初回登録・11月14日限定アップロード・最終導入

- 初期登録用キーで匿名ユーザーと端末を登録し、発行された機器用キーの `GET users/me` と管理用キーの `GET openapi.json` を200で確認した。実値と復旧コードはignored `docs_private/numbershare-credentials.json` にだけ保存した。
- 422の原因は `observed_at` の小数秒が9桁だったことだった。API仕様に合わせてミリ秒3桁へ固定し、既存blocked項目を自動再送せず、明示した期間内の項目だけを再送待ちへ戻すよう修正した。
- リアルタイム送信を無効のまま維持し、JST `2026-11-14`〜`2026-11-14` の期間指定アップロードを実行した。対象95件だけを再送待ちへ戻し、最終状態は送信済み95件・待ち0件・blocked 0件。キューの全95件がJST 11月14日であることをread-backした。
- `APIから全件受信` はGETだけを使用する。実機から再受信し、自分の検知95件、全件JST 11月14日、`truncated=false` を確認した。
- NumberShare、VOICEVOX、NCC保存Wi-Fi、ホットスポットの保存済みキー／パスワードを所有者UIへ読み戻す。入力欄は既定で伏せ字、明示的な表示操作時だけ平文にするE2Eを通した。
- 管理用キー自体は有効だが、公開管理画面は初回一覧GETに空の `category=` を必ず付け、APIが422 `種別が不正です` を返すためログイン完了表示にならない。キーなしカテゴリ条件は200、有効カテゴリ条件も200であり、管理画面側の空条件送信が原因と切り分けた。
- 最終成果物と実機バイナリのSHA-256一致、Web/Broker `NRestarts=0`、AP・モデム・native workerの同一性、純正サービス、前方／車内JPEG、SD Range、秘密情報除外を確認した。Go全パッケージtest/vet、postboot 142件、NumberShare実機E2E、対象合成E2Eに合格した。全E2Eは60件成功・204件スキップ・既知のVite専用地図検査11件のみ失敗した。
