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

- `0x1658b8` から始まる VideoEncodeStreamer の構築処理は、コンストラクタ初期値として 1280x720、16 fps、エンコード方式 ID 9、ビットレート方式 2、4 Mbps、キーフレーム間隔 1000 ms を保持する。実機起動時は純正設定反映後に1920x1080でstreamerを生成する。
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

当初の安全境界では実装対象外だったが、その後「純正ファイルは変更せず、`DriveRec` の起動時フックとプロセスメモリ変更のみ」を例外として明示的に許可された。この例外の範囲で 2 と 3 を組み合わせて実装した。純正バイナリの置換、QMMF の競合利用、公開されていない command の捏造は行わない。

### 起動時フックの実装

- `app-@DriveRec.service` の純正 unit は変更せず、NCC 所有の `90-ncc-recorder-hook.conf` drop-in だけで起動フックを設定する。実行不可マウントの永続領域から `ExecStartPre` で `/run/np1-control-recorder-hook` へ一時コピーし、そのコピーを `LD_PRELOAD` する。起動のたびにフックが新しいプロセスへ読み込まれ、プロセス終了とともに一時コピーとメモリ変更は消える。
- 既存の所有者用復旧サービス構成は `tools/device/recorder-hook-update.sh` で更新する。NCCバイナリ、broker unit、hook、drop-inを個別バックアップし、失敗時はすべて復元する。更新そのものでは `DriveRec` を再起動せず、純正6サービスのPID・InvocationID・状態を更新前後で比較する。
- preload ライブラリは、`/proc/self/exe` が上記 SHA-256 と一致し、`StartOrdinaryRecording` 2種類と `StopOrdinaryRecording` の先頭命令16バイトが解析済みの値と一致した場合だけ、AArch64 の絶対ジャンプへ置き換える。未知のファームウェアでは純正動作を維持する。
- `NPMPS_CreateVideoEncodeStreamer` を interpose し、実機で観測した純正既定値 10,000,000 bps を前方（通常・タイムラプス）、4,000,000 bps を車内として識別する。各カメラのターゲットビットレート、CBR/VBR、GOPミリ秒を、streamer 作成前のエンコード構造体へ反映する。
- `DriveRec` の `0x165fa8` 呼出し直前の値コピーと純正ログ文字列を突き合わせ、`NPMultimediaPlayerVideoEncodeProperty` のレイアウトを `frameRate:+0 / width:+8 / height:+12 / codecType:+16 / bitrateType:+24 / bitrate:+28 / KeyFrameInterval:+32` と確定した。実機診断で最初に `+24` を読み `2`（VBR）となったため、ビットレートは `+28` へ訂正した。実機でエンコード解像度 `1920x1080`、codec enum `9` がH.264であることも確認した。書換え時はこの解像度、H.264、純正既定ビットレートを同時に検証し、いずれかが異なる場合は純正値を保持する。
- 車内録画 OFF では、車内カメラに対する通常録画の start / stop を成功応答で抑止し、前方カメラは純正処理へ委譲する。これは通常連続録画の経路に限定した実装であり、イベント録画や駐車録画まで停止することは未確認である。
- テキストページの保護を RX に戻せない場合は元命令へ復元する。さらに RX 復元も失敗した場合はプロセスを終了し、RWX のまま録画を継続しない。

### 設定・適用・read-back

- Web の「H.264詳細録画設定」で、前方／車内それぞれ 128～50,000 kbps、CBR/VBR、GOP 250～10,000 ms と、車内通常録画 ON/OFF を設定できる。
- 設定は CRC32 と generation を持つ64バイトのNCC専用ファイルへ atomic 保存する。純正設定ファイルは変更しない。
- 即時適用は録画が数秒中断することを利用者が確認した場合だけ `DriveRec` を安全な既存サービス操作経路で再起動する。保存と再起動確認を分離し、再起動失敗を成功表示しない。
- read-back は active 状態だけでは合格にしない。実行中 `DriveRec` のSHA-256、preload ライブラリの実行マッピング、3か所のジャンプ命令、ジャンプ先が当該ライブラリ内であることを `/proc/<pid>/mem` から確認する。
- H.264 profile / level は今回の対象外とした。音声 ON/OFF は既存の純正 function `0x600` command 4 を継続利用する。

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
- 実機へ再現可能な `recorder-hook-update` として配備した。更新処理では `DriveRec` を再起動せず、純正6サービスの状態・PID・InvocationIDが不変であることを確認した。適用APIを明示実行した場合だけ `DriveRec` を再起動した。
- 実機の任意値試験では、前方 `8,000 kbps / CBR / GOP 1,500 ms`、車内 `3,000 kbps / VBR / GOP 2,000 ms` をstreamer作成直前にread-backし、前方・車内のH.264 encoding successを確認した。前方には通常録画とタイムラプスの2ストリームとも同値が反映された。
- 車内通常録画 OFF を適用し、APIのgenerationと設定read-back、3か所のメモリフック、および実行時の `cabin ordinary stop suppressed` を確認した。SD未挿入（`insertSts:2`）のため、通常録画開始抑止とMP4ファイル差分は確認できていない。試験後は前方 `10,000 kbps`、車内 `4,000 kbps`、VBR、GOP `1,000 ms`、車内通常録画 ON の純正相当値へ復元した。
- 実行中の永続領域／`/run` ライブラリSHA一致、`LD_PRELOAD`、実行マッピング、純正 `DriveRec` のSHA、3か所のジャンプ命令とジャンプ先をread-backした。Communication / DriveRec / ImageRecog / Voice / QMMF / NEventWatcherCom はすべて active、`DriveRec` の `NRestarts=0` だった。
- ADBは利用者が接続した対象実機だけに固定して使用した。Windows側Wi-Fiの接続先・プロファイル・設定には触れていない。
