# ドラレコ録画設定・低遅延ライブビュー・Wi-Fi 自動切替

## 実装した機能

- 録画設定本体を「システム > 録画設定」へ配置した。録画再生画面の「録画設定」は同タブへのリンクにした。
- 前方／車内を個別に、任意ビットレート（128～50,000 kbps）、VBR/CBR、GOP（250～10,000 ms）へ変更できる。
- 車内録画を個別にON/OFFできる。OFF時は車内の圧縮済み出力だけを録画先へ渡さず、前方録画は継続する。ONへ戻すと同じプロセスで再開する。
- 前方／車内共通の通常録画分割時間を30～600秒へ変更できる。
- 音声ON/OFF、前方／車内画質プリセット、衝撃感度、情報重畳は純正Setting function `0x600`を継続利用する。
- 録画再生画面は、URLに明示時刻がない場合、その表示条件に合う最新ファイルの開始位置を初期選択する。
- 純正H.264エンコーダー直後のaccess unitを再エンコードせず、H.264/RTP over WebRTCでライブビューへ渡す。SD上のMP4、fragmented MP4、MSE、HLS、MPEG-DASHはライブ経路に使用しない。
- JPEGライブビューは軽量640x360と高解像度1920x1080を選択できる。どちらも既存の純正NV12 exportを読み、カメラdeviceや録画pipelineは開かない。
- H.264 profile / level指定は今回の対象外とした。

## 安全境界

- 対象`DriveRec`のSHA-256は`000871dc36581f27ae831d9db3772b553a17cce0d814c52867276ac68ea0cde0`に固定する。
- rootfs、dm-verity、raw block、純正バイナリ、純正unit、純正設定ファイルは変更しない。
- 旧`LD_PRELOAD` drop-inは削除し、`app-@DriveRec.service`の起動定義は変更しない。
- NCC native workerが実行中`DriveRec`へ固定SHAのヘルパーでフックを注入する。プロセス終了時に変更は消え、次の起動ではworkerが再適用する。
- 未知のSHA、命令列、GOT、vtable、オブジェクト型では書換えず、適用済み表示にもしない。
- 純正app unitは相互依存しているため、個別の`systemctl restart`を行わない。録画設定の即時適用はプロセス内再適用だけで行い、通常起動の復旧が必要な場合は純正サービス制御APIまたは本体の通常起動順を使用する。
- Windows側のWi-Fi接続先、プロファイル、接続状態は変更していない。ADBは利用者指定serialだけへ固定した。

## プロセス内の録画設定適用

- `libNPMultimediaPlayerStack.so`の`PMF_GStreamerVideoEncoder::setH264EncParameter()`を解析し、稼働中オブジェクトの固定フィールドを更新して純正のGStreamer設定関数を呼ぶ。
- 実機には前方通常・前方タイムラプス・車内の3オブジェクトがある。vtable、H.264 codec ID 9、解像度、既定ビットレート、pipeline pointerを同時検証して識別する。
- 固定実機のライブtapでは、前方は選択された純正出力により1920x1080/27fpsまたは1280x720/27fps、車内は1280x720/16fpsだった。純正画質ラベルではなく実際のSPS／エンコーダー値を表示・試験する。
- 分割時間は`DriveRec + 0x14c774`の`mov w6, #seconds`と、稼働中の前方／車内録画オブジェクト`+0x254`を同時更新する。命令形式を検証し、ページをRXへ戻せない場合は元命令へ復元する。
- 即時適用では`DriveRec`を再起動しない。注入ヘルパーは全スレッドのレジスタとスタックを保存してフックを読み込み、完全復元後、通常スレッドを動かした状態で再適用workerだけを起動する。
- 過去に試した「純正画質を別プリセットへ変更して元へ戻し、エンコーダーを再生成する」方式は負荷暴走とwatchdog rebootを招いたため廃止した。現在のコードと配備物には含めない。

## 厳密read-back

適用済みgenerationを確定する前に次を確認する。

- 実行中`DriveRec`とランタイムフックのSHA-256
- 3か所の録画開始／停止jump、streamer GOT、圧縮出力vtableのジャンプ先
- 前方2系統と車内1系統のビットレート、VBR/CBR、GOP
- 車内録画ON/OFFのフック状態
- 分割時間のAArch64命令と前方／車内録画オブジェクト
- SD挿入時は前方MP4の増加、車内ON時は車内MP4の増加、車内OFF時は車内MP4の停止
- `DriveRec`のworker threadと純正サービスのactive/running状態

プロセスがactive、またはライブラリがmapsに見えるだけでは合格にしない。

## エンコーダー直結ライブビュー

- `MPS_StreamBuffer::write`のvtable slotを、固定ライブラリと元関数を照合してプロセスメモリ上だけ差し替える。
- SPS/PPSとIDRを含むH.264 access unitを前方／車内のUnix datagram socketへ送る。60KB単位、`MSG_DONTWAIT | MSG_NOSIGNAL`とし、閲覧側が遅くても純正録画を待たせない。
- brokerはカメラごとにUnix datagramを常時drainし、最新GOPだけを上限付きで保持する。新しい閲覧はキャッシュ済みの最新IDRから開始する。
- ブラウザーとは非trickle ICEでSDPを交換し、H.264 access unitをPion WebRTCからRTP/UDPで送る。映像データをHTTP/TCPへ流さず、1フレームごとのMP4 fragmentも生成しない。
- 通信が映像ビットレートへ追いつかない場合は、依存する古いPフレームを破棄し、PeerConnectionを張り直さず最新IDRから再開する。
- 一度映像を表示した後の一時的な`waiting`イベントでは待機パネルを被せない。初回表示前だけ700msの猶予後に待機表示を出し、再接続中は直前の映像面を維持する。
- SDカードの有無にライブビューは依存しない。実機Playwrightで前方1920x1080と車内1280x720のH.264を同時デコードし、フレーム進行、単独／並列切替を確認した。

## Wi-Fi 自動切替

- 自動モードで4G利用中でも、検索または接続操作中だけSTAを起動する。
- supplicant準備完了を待って検索し、回線が一度切れても再接続後に結果を取得する。
- 新規接続は対象ID、SSID、IPv4を照合してから成功とし、処理中は自動切替を抑止する。
- 失敗、取消、タイムアウト時は開始前の回線状態へ戻す。
- NCC保存済みプロファイルはSTA停止中も表示し、起動後にメモリ上へ復元する。4G固定モードではSTAを起動しない。

## UI

- 「即時適用する」がOFFでも保存でき、ボタンを「保存して次回起動時に適用」とする。ONでは「保存して即時適用」とし、録画中断の注意確認を兼ねる。
- 次回起動用の保存は、その時点のDriveRec PIDとプロセス開始時刻をCRC付きで記録する。定期reconcileは同じプロセスへ適用せず、次のDriveRecプロセスでだけ設定を適用・read-backする。
- 利用者向け表記を「即時適用する」「保存して即時適用」に統一し、内部実装や未対応項目の説明は画面から除いた。
- 純正録画設定カードとH.264詳細設定カードは同じgrid内で`18px`の間隔を持つ。
- 適用中は二重送信を防ぎ、generation競合、read-back不一致、録画確認失敗を成功表示しない。

## 2026-11-18 実機最終検証

- 配備フックSHA-256: `29e2984c16ad7501d3b8bfac445fa2dd96ae70749d4b396dc1403c1dd78c5bf6`
- 注入ヘルパーSHA-256: `c79616d51adee1560d35dd5cf2a54d4fb3ed28ee616c316667ca7b8eaa280d29`
- 最終generation: `51`
- 最終設定: 前方`5,000 kbps / VBR / GOP 1,000 ms`、車内`1,000 kbps / VBR / GOP 1,000 ms`、車内録画ON、分割180秒
- 同一`DriveRec` PIDのまま前方2系統と車内1系統へ即時反映した。CBR、異なるGOP、分割240秒、車内OFF/ONも実機で変更し、すべて最終値へ復元した。
- 車内OFFではSDファイル進行停止、ONでは再開を確認した。分割240秒は命令と録画オブジェクトをread-backし、180秒へ復元した。
- 本体再起動後と今回の更新後に、`hookLoaded:true`、`applied:true`、`restartRequired:false`を確認した。現在値も前方5,000,000 bps、車内1,000,000 bps、VBR、GOP 1,000 msとして厳密read-back済みである。
- Communication / DriveRec / ImageRecog / Voice / QMMF / NEventWatcherComはすべて`active/running`、`Result=success`、`NRestarts=0`。
- hardware verifyは前方JPEG、車内JPEG、実MP4 Range/seek、SD、パストラバーサル拒否、秘匿を合格した。
- 録画再生・設定UIのPlaywright 7件は合格。実機H.264並列ライブビューも合格した。
- 全E2Eは61件合格、11件失敗、210件skip。11件は今回の録画／Wi-Fi機能ではなく、地図fixtureが`/src/services/vector-map.ts`を動的importできない同一原因だった。
- 本体master volumeは再起動後も`0`を純正Setting応答で確認した。

## 2026-11-18 ライブビュー遅延・ちらつき再修正

- 実機tapをSD非依存で直接計測し、前方217フレーム/8.04秒（約27.1fps）、車内129フレーム/8.04秒（約16.0fps）、不正datagram 0件を確認した。エンコーダ出力自体は実時間で連続していた。
- 前方・車内の同時表示を30秒継続し、HTTP再接続0回、MSE live edgeとの差は約0.10～0.23秒で推移した。
- 前方設定5,000kbpsに対して回線を3,000kbps・遅延100msへ制限した20秒試験でも、待機パネルの再表示0回、20サンプル中15以上でlive edge差0.8秒未満を確認した。
- 次回起動適用は同じ設定値でgeneration 49→50を保存し、7秒後も同一DriveRec PIDのまま`applied:false`／`restartRequired:true`を確認した。その後、同じ値を即時適用してgeneration 51、`applied:true`へ戻し、設定値が不変であることを確認した。

## 2026-11-18 Tailscale経路のH.264遅延対策

この節のMSE方式と計測値は後述のWebRTC移行で廃止した経緯であり、現在の実装には使用しない。

- MSEのbuffer末尾との差だけでは、Tailscale内の送信待ちに映像全体が滞留した場合を検出できないため、接続後の実経過時間と受信したmedia timelineの進行量を併せて監視する。
- media timelineが実時間より1.25秒を超えて遅れた接続は破棄し、150ms後に現在のIDRへ再接続する。通常の切断も500msで再接続し、再接続中は最後の映像面を維持して待機表示を重ねない。
- H.264要素にテスト用のtransport ageを公開し、帯域制限下でも値が1.25秒以内であること、MSE live edgeとの差が0.8秒未満であること、古い接続を実際に更新したことを実機E2Eで検証する。
- Tailscale経由を600kbps・100msへ制限した20秒の実機試験では、古い接続を6回更新し、transport age最大1.146秒、再生中のMSE live edge差最大0.032秒、待機表示の再出現0回だった。
- 帯域制限なしの前方・車内同時表示を60秒継続し、120サンプルのtransport age最大0.236秒、MSE live edge差0.042～0.505秒、途中再接続0回、両映像の連続デコードを確認した。

## 2026-11-19 WebRTCへ全面移行

- 1 access unitを1個の`moof/mdat`にするHTTP/fMP4/MSE経路と公開APIを削除した。ライブ経路はSDP用の短いHTTP POSTと、映像用のH.264/RTP over WebRTCだけである。
- encoder tapは常駐hubが受信し、最大300フレーム／12MiBの範囲で最新GOPだけを保持する。閲覧開始時に新しいUnix socketやSD追尾を作らない。
- ランタイム注入でメタデータのfpsが0の場合は、受信フレームを1秒以上実測してRTP sample durationを決める。SPS/PPSは各IDRでも再送し、broker再起動後は次のIDRで自己復旧する。
- WebRTC UDPは管理インターフェース上の50000～50019だけを許可する。同時PeerConnectionは前方・車内の2本に制限し、15分またはセッション削除で閉じる。
- ブラウザー終了時はkeepalive付きDELETEを送り、ICE切断が5秒続いたPeerもbroker側で回収する。同じカメラの新しい表示要求は残存Peerを即時置換するため、異常終了したタブが前方・車内の2枠を塞がない。
- 実機ブラウザーで前方1920x1080、車内1280x720を60秒同時デコードし、前方1,765フレーム、車内1,013フレーム、SDP応答各1回のまま連続した。スクリーンショット保存時刻`00:42:00`と両映像内の純正時刻表示`00:42:00`が一致し、従来の6～8秒遅延がないことを確認した。
- SDP用HTTP応答だけを1.5秒遅延させた20秒試験も、待機表示の再出現なしで合格した。その直後に前方・車内同時表示を開始でき、残存Peer回収も確認した。
- TailscaleのHTTPS経路でも前方・車内同時表示を10秒継続し、切断・再シグナリングなしで合格した。

## 復旧容量

- クラッシュダンプは0件だった。容量圧迫の主因はNCC復旧世代だった。
- 固定recoveryディレクトリを`readlink -f`で検証してから削除するtoolを使用し、recorder-hookとweb-workerをそれぞれ最新完全世代1件だけ保持する。
- 最終配備後に旧recorder-hook世代9件と旧web-worker世代4件を削除した。約219MiBを回収し、`/overlay`は使用率18%、空き約1.4GiBになった。
