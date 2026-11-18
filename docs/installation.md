# 導入手順

## 前提

- 所有するNP1へroot ADBで接続できること。root化の手順や純正ファームウェアは本リポジトリに含めません。
- 対象はAArch64 Linux、systemd 239の環境です。ファームウェア202404231204で確認しています。
- ホストにNode.js 24、Go 1.26以降、Python 3、ADB、Gitを用意します。通常ビルドにPythonの追加パッケージは必要ありません。
- カメラ・録画・走行履歴には、読み取り可能なSDカードと関連する純正サービスが必要です。

## ビルドと配備

```powershell
git clone https://github.com/sochov/NP1_mod.git
cd NP1_mod
powershell -NoProfile -ExecutionPolicy Bypass -File tools/build.ps1
adb devices
adb -s SERIAL shell id
python tools/deploy.py --serial SERIAL
python tools/connect.py --serial SERIAL
```

`id`がrootであることを確認します。Goのパスを指定する場合は `tools/build.ps1 -Go PATH_TO_GO`、依存インストール済みの場合は `-SkipInstall` を使えます。Linux / macOSのビルドは `sh tools/build.sh` です。

既設の所有者用 `np1-owner-recovery.service` が起動時に `/etc` overlay を読み直す機体では、初回導入に `python tools/deploy.py --serial SERIAL --action install-owner-recovery` を使えます。この方式は同サービスの `Wants` に専用unitsを登録し、専用の数値 UID/GID でWebを実行します。既設の起動スクリプト、アカウントファイル、OTA設定は導入前の状態を維持します。APとLEDの自動起動は所有者が各機能で設定した場合に有効になります。Wi-Fi / 4Gの回線切替は所有者が回線設定を初めて保存すると稼働します。この方式の復旧には `--action rollback-owner-recovery` を使用します。

ビルドはフロントエンド、Goのテストと静的検査、Linux ARM64バイナリ、ナンバー認識のモデルと推論エンジン、Tailscale、ライセンス一覧、配備用manifestを生成します。Tailscaleは固定バージョンの公式AArch64配布物を取得し、公開SHA-256と照合してから成果物へ同梱します。成果物は `artifacts/ncc/` に置きます。配備はハッシュを確認し、変更前の復旧コピーを本体へ保存します。本体のNode.js常駐は不要ですが、導入・起動スクリプトは既存のPython 3を使用します。

## アクセス

| 経路 | URL |
| --- | --- |
| ADB転送 | `http://127.0.0.1/` / `https://127.0.0.1/` |
| 管理LAN | `http://本体の管理IP/` / `https://本体の管理IP/` |
| Tailscale登録後 | `http://本体のTailscale IP/` / `https://本体のTailscale IP/` |

本体は標準ポート80 / 443を使います。ホストの同じポートが使用中なら、`python tools/connect.py --serial SERIAL --http-port 8080 --port 8443` のようにホスト側の転送先を指定できます。HTTPSは自己署名証明書のためブラウザーで警告される場合があります。`tools/connect.py` の指紋はADB経由で取得します。Webパスワードはなく、管理ネットワークへ接続できる端末が利用できます。

## 初期状態

常時テザリング用の専用APと、管理Webの起動処理を追加します。Wi-Fi / AP画面でSSID・認証・DHCPと実際の上流を確認してください。AP設定の変更時には無線接続が一時切断する場合があります。

標準インストーラーでは本体の自動アップデートを初回導入時に無効化します。システムの設定画面から有効に戻せますが、対応する純正ABIが変わる可能性があるため非推奨です。走行ロガーは動作し、有効な測位データが得られた分を記録します。NASの自動バックアップはOFFです。

ナンバー認識の認識サービス（`np1-control-plate.service`）、学習済みモデル、推論エンジンも導入します。ナンバー画面で記録を有効にするまで認識は動作しません。読み上げを使う場合は、システム › LED / 音声でVOICEVOX APIのキーを設定してください。詳細は [ナンバー認識と発話](plate-recognition.md) を参照してください。

## Tailscale

通常のビルドと初回導入でTailscale本体とサービスも導入されます。認証情報はビルド成果物に含めず、自動ログインもしません。初回導入後、WebのTailscale画面から認証を開始し、表示されるリンクで所有者のtailnetへ登録します。既に登録済みの端末を更新する場合、保存済みstateと実行中サービスを通常のNP1CC更新で変更しません。

旧版のNP1CCを導入済みでTailscaleだけが未導入の場合は、通常ビルド後に `python tools/deploy.py --serial SERIAL --action install-vpn` を一度実行します。この移行も既存のTailscale stateがあれば保持します。DNSの上書き・他ノードのルート受入・Tailscale SSH・subnet広告は初期設定で無効です。既存のADB listenerがある対応環境では、専用firewall設定がTailscaleインターフェースからの接続を許可します。ADB listenerを新設する機能ではありません。

## 純正連携ヘルパー

通常の公開ソースだけでWeb / brokerをビルドできます。純正設定の変更や一部の純正サービス起動には、別途ビルドする専用ヘルパーが必要です。ヘルパーのソースは `tools/native/`、ビルド入口は `tools/build-native-*.py` と `tools/build-stock-start-helper.py` です。

この追加ビルドにはClang / LLD、Node.js、pyelftoolsと、所有する対応端末のリンク用ファイルが必要です。必要なファイル名は各ビルドスクリプトに記載しています。取得したファイルは `resources/native-sysroot/` に置き、公開・再配布しないでください。生成済みヘルパーがある場合、通常ビルドが配備物へ取り込みます。端末の純正ライブラリは実行時に照合し、未知のファームウェアには適用しません。

更新や取り消しは [復旧手順](recovery.md) を参照してください。
