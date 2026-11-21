# 更新・復旧・アンインストール

以下のコマンドはリポジトリのルートから実行します。`SERIAL`は対象NP1のADB識別子です。操作は `tools/device/` のスクリプトで再現できます。

## 状態の確認

```powershell
python tools/deploy.py --serial SERIAL --action verify
```

Webが開かない場合は、ADB接続と管理IP、専用サービスの状態を先に確認してください。自己署名証明書が原因の場合は `http://本体の管理IP/` からも接続できます。

```powershell
adb -s SERIAL shell systemctl status np1-control.service np1-control-broker.service
python tools/connect.py --serial SERIAL
```

## 更新

更新サービスを導入済みの場合は、管理画面から署名済みの互換版を取得できます。初回導入、更新モード、配布手順は [NP1CCの自動更新](np1cc-auto-update.md) を参照してください。

通常の更新は、最新版のソースをビルドして再配備します。

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File tools/build.ps1
python tools/deploy.py --serial SERIAL
```

既に導入済みで、変更対象がWeb / brokerだけの場合は次の更新も利用できます。AP・モデム・純正サービスを更新する代わりには使えません。

```powershell
python tools/deploy.py --serial SERIAL --action web-worker-update
```

Web更新中は開いている画面やライブ配信が一時切断します。設定が変わらないのに接続を繰り返し失う場合は、ブラウザーだけでなく専用サービスのログと本体の起動状態を確認してください。

所有者用復旧サービス経由の導入には `web-worker-update` を使用できます。初回導入全体を取り消す場合は `python tools/deploy.py --serial SERIAL --action rollback-owner-recovery` を実行します。専用unitsと起動時の依存登録を取り除き、設定・状態と検証記録を `/overlay/owner-apps/np1-control.removed-<UTC時刻>` に保存します。

## ロールバック

```powershell
python tools/deploy.py --serial SERIAL --action rollback
```

導入時の復旧コピーは本体の `/overlay/owner-apps/np1-control/recovery/` に保存します。ロールバックは直前の本プロジェクトのバイナリ・unit・設定を対象にします。初回導入の取り消しはアンインストール相当です。純正ファームウェア全体を復元する機能ではありません。

## アンインストール

```powershell
python tools/deploy.py --serial SERIAL --action uninstall
```

OTAの見張り役を止め、更新処理の通信遮断チェーンと停止フラグの変更不可属性を外してから、専用unit（ナンバー認識の `np1-control-plate.service`、OTAの見張り役 `np1-control-ota-guard.service` を含む）、管理対象のpostbootブロック、専用firewall設定、導入時に作成したnccアカウントを撤去します。ナンバーの記録・画像・発話用の音声とVOICEVOX APIキーは所有者のデータとして `state/plate/` に残ります。不要な場合は手動で削除してください。OTA抑止は管理記録に基づいて戻します。所有者の設定・資格情報・Tailscale state・復旧コピーは残します。SD録画やNASの既存ファイルは削除しません。

ホストから配備できない場合も、本体の `/overlay/owner-apps/np1-control/tools/` に復旧用スクリプトを保持します。rootシェルから `/bin/sh` で実行してください。
