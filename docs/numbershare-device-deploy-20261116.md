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
- NumberShare実機専用GET-onlyテストで、共有タブ、書き込み専用キー入力、初期キュー、モバイル幅の非オーバーフローを確認し、1件成功した。

本番NumberShare APIのエンドポイントと認証キーは未設定のままにした。外部APIへの検知送信や共有データ取得は行っていない。
