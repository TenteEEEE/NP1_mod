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
- `NPMPS_CreateVideoEncodeStreamer` を interpose し、実機で観測した純正既定値 10,000,000 bps を前方（通常・タイムラプス）、4,000,000 bps（1080p）または2,000,000 bps（720p）を車内として識別する。各カメラのターゲットビットレート、CBR/VBR、GOPミリ秒を、streamer 作成前のエンコード構造体へ反映する。
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

## 通常録画ファイルの分割時間

- 固定対象の純正 `DriveRec` を静的解析し、`CDVR_ENT_RECCORE_OrdinaryRecorder` のコンストラクタ `0x14c6b8` が `0x14c774` の `mov w6, #180` で通常録画の分割時間を180秒に初期化し、オブジェクトの `+0x254` へ保存することを確認した。
- 同クラスの `SetRecFileDuration` は `0x14c878` にあり、引数の32bit値を同じ `+0x254` へ保存する。通常録画の前方・車内で共通の秒単位設定である。
- 純正ファイルは変更せず、起動時フックがSHA-256と元命令 `86 16 80 52` を確認した場合だけ、`0x14c774` の即値を設定値へプロセスメモリ上で置き換える。対象範囲は30〜600秒とし、未設定・旧形式の予約値0は純正値180秒へ移行する。
- 64バイトの既存設定形式を維持し、offset `+44` を `segmentDurationSeconds` に割り当てた。Web APIと画面へ「録画ファイルの分割時間」を追加し、前方・車内の通常録画へ共通適用する。
- read-backは従来の3か所の録画開始・停止ジャンプに加え、実行中 `DriveRec` の `imageBase + 0x14c774` が設定秒数を表すAArch64命令になっていることまで確認する。プロセスがactiveであるだけでは適用済みにしない。

## SD未挿入時のライブビュー調査

- JPEGライブビューはSDなしでも取得でき、実機でHTTP 200のJPEGフレームを確認した。
- 従来のH.264ライブビューは純正SD上のMP4を追尾していたため、SD未挿入では `CAMERA_H264_UNAVAILABLE` になっていた。現在は後述のエンコーダ直結経路を優先するため、ライブビュー自体はSDを必要としない。
- ただし純正 `DriveRec` が車両状態やSD異常を理由にエンコーダを停止した場合は入力が存在しない。この場合は通信速度とは無関係に「カメラ出力の再開を待っています」となり、出力再開後に自動接続する。

## 追加検証

- フック設定parser・ARM64ビルド、frontend build、全Go test/vet、Python unittestに合格した。
- ローカルViteを使った録画設定・再生E2Eは7件すべて合格し、分割時間240秒の送信と、純正設定カード／詳細設定カード間の16px以上の余白を検証した。
- 実機配備時は `recorder-hook-update` を使い、更新前後の純正サービス状態を比較する。分割時間240秒でメモリread-back後、180秒へ復帰し、Communication / DriveRec / ImageRecog / Voice / QMMF / NEventWatcherComを最終確認する。

### 分割時間の実機検証結果

- `recorder-hook-update` で新しいWeb／broker／起動フックを配備した。更新処理は `DriveRec` を再起動せず、復旧バックアップを作成して正常終了した。
- 配備前から利用者が設定していた前方 `2,000 kbps`、車内 `1,000 kbps`、VBR、GOP `1,000 ms`、車内録画ONを保持したまま、分割時間だけを240秒へ変更した。APIは generation 15、`hookLoaded:true`、`applied:true` を返した。
- 実行中 `DriveRec` の `imageBase + 0x14c774` は240秒時に `06 1e 80 52`、起動ログは `ordinary segment duration applied seconds=240` となり、設定値のプロセスメモリ反映を独立に確認した。
- 検証後は分割時間だけを純正相当の180秒へ戻した。APIは generation 16、`restartRequired:false`、`applied:true` を返し、同アドレスは純正命令 `86 16 80 52` へ戻った。
- 永続領域と `/run` のフックSHA-256はともに `30bec62fe052808da9ad17d08be32e060143440f2543523a3bb2f75fe2dd0d40` で配布物と一致した。
- SD未挿入状態でも前方JPEG 49,286 bytes、車内JPEG 5,154 bytesを取得した。SD範囲検証は `NO_SD` としてskipし、Webパスワードなし、パストラバーサル拒否、バックアップ情報の秘匿も合格した。
- Communication / DriveRec / ImageRecog / Voice / QMMF / NEventWatcherCom はすべて `active/running`、`Result=success`、`NRestarts=0` だった。

## エンコーダ直結の低遅延ライブビュー

- `libNPMultimediaPlayerStack.so` の `MPS_StreamBuffer` vtableにある圧縮済みaccess unit書込みを、対象ライブラリのロードアドレスと固定offsetを検証した上でプロセスメモリ上だけ差し替える。純正ライブラリ、`DriveRec`、設定ファイルは変更しない。
- フックはSPS/PPSとIDRを含むH.264 access unitを前方／車内の固定Unix datagram socketへ送る。60KBに分割し、`MSG_DONTWAIT | MSG_NOSIGNAL`を使うため、閲覧側の遅延や切断で純正録画スレッドを待たせない。欠落時は次のIDRまで配信を止め、破損したGOPを表示しない。
- Web/API側はaccess unitを再エンコードせず、Annex BからAVCCへ変換して1 access unitずつfragmented MP4へ格納する。応答の `X-Camera-Source: encoder` でSD追尾ではないことを確認できる。旧プロセスとの移行互換として、直結経路が起動前の場合だけ既存MP4追尾へフォールバックする。
- 初回IDRは5秒まで待ち、配信開始後に4秒間出力がなければHTTP応答を終了する。ブラウザは1.5秒間隔で再接続し、再生位置をbuffer末尾の約0.25秒以内へ追従させる。純正エンコーダが休止しても古い接続を保持し続けない。
- 実機でエンコーダ稼働中の前方を直接取得し、`1920x1080`、H.264 `avc1.428028`、約27fps、映像のみ、60フレームをffprobeで確認した。約3MBの保存データはffmpegでエラーなく全フレームをデコードできた。映像・生ログはignored `tmp/`だけに保存した。
- 検証台の最終試験ではSD容量状態を純正側が異常と判定し、通常録画を開始直後に停止した。`DriveRec`ログの「Failure to secure SD free space」と各encoder thread終了を確認しており、回線速度による待機ではない。

## 録画再生と設定画面の導線

- 日付を開いた時、URLに明示的な `at` がなければ、現在のカメラ表示条件に合う最新ファイルの開始時刻へシークする。URL指定がある場合は従来どおりその時刻を優先する。
- 録画設定本体は「システム > 録画設定」タブへ移した。ドラレコ再生画面右上の「録画設定」は `#system/recorder` へのリンクであり、再生画面内に設定フォームを重複表示しない。
- Playwrightでは最新前方ファイルの時刻への初期シーク、設定リンクからシステムタブへの遷移、任意ビットレート・車内録画OFF・分割時間の送信を検証する。

## 2026-11-18 最終配備記録

- `recorder-hook-update` でWeb/APIと起動フックを配備し、永続領域と `/run` のフックSHA-256が `c6d4a270733366bbccbaf3191d4c7feed5ebd983041c4c96f67b44f547b764fa` で一致した。NCC本体も永続領域と `/run` で一致した。
- 配備処理は純正サービスを再起動せずに完了し、録画設定の即時適用操作だけが `DriveRec` を再起動した。フックvtable、設定世代、プロセスマッピングをread-backし、`hookLoaded:true` / `applied:true` を確認した。
- 試験中だけ純正相当の前方10,000／車内2,000kbpsを使用し、試験後は利用者設定の前方2,000／車内1,000kbps、VBR、GOP 1,000ms、車内録画ON、分割180秒へ復元した。ブート自動適用の最終generationは26。
- 配備途中にoverlay空き容量が不足した際は、当該作業中に生成された不完全な復旧世代3件だけを削除し、完全な復旧世代からNCCを復元した。その3件は復元不能だが、成功配備後の完全な復旧バックアップを保持している。
- Windows側のWi-Fi接続先、プロファイル、接続状態は変更していない。ADBは利用者指定の実機だけに固定した。

## 本体案内音量

- 利用者指示により純正 function `0x400` のmaster volumeを1から0へ変更した。変更要求の相関応答はタイムアウトしたため再送せず、純正起動系列を復旧した後の独立GETで0を確認した。
- 対象実機を1回再起動し、純正 `Setting` の通常起動完了後にもmaster volumeが0であることをread-backした。プロセス内だけの一時変更ではなく、再起動後も維持される保存設定である。
- この過程で `tools/reboot-test.py` が `verify.sh` をstageしていない不具合と、再起動直後にハードウェア検証を1回しか試さない競合を修正した。再起動前の検証を必須にし、復帰後は最大90秒だけ検証可能状態を待つ。

## ブート時の録画フック自動適用

- 実機では純正 `DriveRec` がブート直後に起動し、永続 `/etc` overlay上のNCC所有drop-inがsystemdから見えるのはNCC broker起動時だった。この順序では、drop-in自体が永続でも初回 `DriveRec` プロセスには `LD_PRELOAD` されない。
- broker起動8秒後にフックのプロセスメモリread-backを1回行い、未ロードの場合だけ既存の型付きサービス操作で `DriveRec` を再起動する。再起動後は純正AppManager起動バリアを通知し、ライブラリmapping、4か所のメモリフック、設定世代を確認する。
- この自動調停はuptime 15分以内に限定した。走行中などにbrokerだけを後から再起動した場合、利用者確認なしで録画を中断しない。
- 配備後に実機を再起動し、ADB、HTTP/HTTPS、前後JPEG、SD range、純正サービスを検証した。その後、手動適用なしで `hookLoaded:true`、`applied:true`、generation 26、前方2,000／車内1,000kbps、分割180秒をread-backした。
- 同じ再起動後にmaster volume 0も再確認した。Communication / DriveRec / ImageRecog / Voice / QMMF / NEventWatcherComはすべてactive/running、`Result=success`、`NRestarts=0`。

## 復旧世代の容量管理

- `/overlay` が99%に達して更新余地がなくなったため、固定ディレクトリを`readlink -f`で検証する `recorder-hook-prune` を追加した。録画フック復旧世代だけを対象にし、最新4世代を残す。
- 実機では古い11世代を削除して172.9MBを確保し、その後の成功配備で新しい完全バックアップを1世代追加した。現在は最新の完全な復旧世代を5件保持し、空き158.4MB。
