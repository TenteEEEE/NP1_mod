# ドラレコ録画設定と自動回線時の Wi-Fi 操作

## 実装済みの録画設定

- Web の「録画設定」から、前方カメラと車内カメラを個別に 1080p / 720p へ変更できる。
- 録画音声を ON / OFF できる。
- 純正 Setting サービスの function `0x600` を使い、設定後は read-back と非対象フィールド不変を確認する。
- 録画一覧は MP4 の映像トラックサイズと時間から平均映像ビットレートを表示する。音声トラックは計算に含めない。

## 任意ビットレート・H.264 詳細設定・カメラ個別停止の解析

### 純正アプリが公開している範囲

公式 Android アプリ 1.6.3 の `DriveRecorderSettingManager` と生成 protobuf を静的解析した。録画設定 function `0x600` の command は次の範囲に限られる。

| command | 内容 | 値 |
| --- | --- | --- |
| 1 | 基本録画設定取得 | 前方画質、車内画質、音声、衝撃感度、情報重畳 |
| 2 | 前方画質 | `REC_HIGH_QUALITY(1)` / `REC_LONG_TIME(2)` |
| 3 | 車内画質 | `REC_HIGH_QUALITY(1)` / `REC_LONG_TIME(2)` |
| 4 | 音声 | OFF / ON |

画質 enum には `REC_DEFAULT(0)` もあるが、カメラ停止を表す値ではない。任意ビットレート、H.264 profile / level / rate-control / GOP、前方または車内だけを停止する command は存在しない。

`DriveRec` に埋め込まれた公開 protobuf も調べた。function `0x601` は SD フォーマット、SD 挿入状態、オプションケーブル、フォーマット状態などで、通常録画のエンコーダ設定やカメラ個別停止は公開していない。

### DriveRec 内部のエンコーダ設定

対象 `DriveRec` の SHA-256 は `000871dc36581f27ae831d9db3772b553a17cce0d814c52867276ac68ea0cde0`。AArch64 の静的解析で以下を確認した。

- `0x1658b8` から始まる VideoEncodeStreamer の構築処理は、既定値として 1280x720、16 fps、エンコード方式 ID 9、ビットレート方式 2、4 Mbps、キーフレーム間隔 1000 ms を保持する。
- `0x165700` の内部メソッドは入力値を `1,000,000` 倍し、エンコード構造体の bitrate フィールドへ格納する。したがって純正内部には Mbps 単位のビットレート設定機構がある。
- 呼出元 `0x161590` / `0x1617d0` は純正設定から得た画質プリセットを解決し、前方／車内それぞれの streamer に値を渡す。公開 command は任意値をこの経路へ渡せない。
- `libNPMultimediaPlayerStack.so` は `target-bitrate` と `interval-intraframes` を OMX H.264 encoder へ設定する。確認できた可変項目はビットレート方式、ターゲットビットレート、キーフレーム間隔で、H.264 profile / level を外部指定する公開経路は確認できない。
- 保存済みサンプルは前方／車内とも H.264 Baseline だった。これは実測結果であり、profile を選べることを意味しない。

### カメラ個別停止

`DriveRec` 内部には `StartOrdinaryRecording(CDVR_ENT_CAM)` と `StopOrdinaryRecording(CDVR_ENT_CAM)` があり、内部状態機械はカメラ単位の開始／停止を扱う。一方、これらは外部公開 ABI ではなく、公開 protobuf や AIPI import に通常録画の個別開始／停止要求はない。

そのため、要求された機能を追加するには、次のいずれかが必要になる。

1. 純正 `DriveRec` バイナリの改変
2. 純正プロセスへのコード注入またはメモリ書換え
3. 純正サービスの起動方法を変更した preload
4. 純正録画を停止し、QMMF を競合利用する別録画サービスへの置換

いずれも現在の開発ルールにある「純正バイナリ・サービス・設定を変更しない」に反し、録画欠落や QMMF 競合の危険がある。この安全境界内では、任意ビットレート、H.264 詳細設定、車内／後方だけの録画停止を実装できない。未確認の command を作って成功表示する実装は行わない。

## Wi-Fi 修正

- 自動モードが 4G へ切り替わった状態でも、スキャンまたは接続操作中だけ STA を起動する。
- スキャンは supplicant の準備完了を待ち、回線が切れた場合も再接続後に結果を取得する。
- 新規接続は対象 ID、SSID、IPv4 を照合してから成功とし、接続中は自動切替を抑止する。
- 失敗、取消、タイムアウト時は開始前の回線状態へ戻す。
- NCC 保存済みプロファイルは STA 停止中も表示し、supplicant 起動後にメモリ上へ復元する。
- 4G 固定モードでは STA を起動しない。

## 検証状況

- frontend の型検査・本番ビルド、backend の `go test ./...` と `go vet ./...`、Python unittest、録画／Wi-Fi／設定画面の Playwright を通過済み。
- ARM64 向け NCC と配布マニフェストを生成済み。
- 実機 `100.111.243.72` では HTTPS の read-only API のみ確認した。録画設定は前方高画質、車内高画質、音声 ON。Communication / DriveRec / ImageRecog / Voice / QMMF は active だった。
- 実機の録画一覧は `NO_SD` のため、新規録画によるビットレート確認はできていない。
- 今回の追加解析では実機設定を変更していない。ADB と Windows 側 Wi-Fi は使用していない。
