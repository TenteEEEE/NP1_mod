// My NP1 user-facing features. Cloud/account and phone-only workflows retain
// their actual prerequisite rather than presenting a nonfunctional Web action.
const guide='https://jpn.pioneer/ja/support/manual/np/np-001/usersguide/'
export const nativeAppFeatures=[
 {group:'映像・撮影',items:[
  {name:'前方・車内カメラを見る',description:'本体のカメラ映像を表示します。',href:'#camera'},
  {name:'SDの録画を再生・ダウンロード',description:'録画日時と走行経路から確認します。',href:'#recordings'},
  {name:'クラウドのイベント映像・写真',description:'My NP1のアカウントとクラウド保存が必要です。',href:guide+'87A1E3D98F7CFE3949A7ED087D730709.php',app:true},
  {name:'イベント録画・写真撮影',description:'純正の音声操作で撮影し、My NP1で保存結果を確認します。',href:guide,app:true},
  {name:'マイカーウォッチ・駐車中の遠隔確認',description:'対応する接続・電源・クラウド契約が必要です。',href:guide,app:true},
 ]},
 {group:'接続・ドライバー',items:[
  {name:'Wi-Fiの接続・保存・削除',description:'本体が接続するWi-Fiを管理します。',href:'#wifi'},
  {name:'クルマWi-Fi',description:'ホットスポットの状態と接続端末を確認します。',href:'#wifi'},
  {name:'Bluetoothの本体連携・登録解除',description:'スマートフォンのBluetoothとMy NP1で連携します。',href:guide+'DF00E87DC78822401BC97A91F16258A2.php',app:true},
  {name:'認証情報の取得・サブドライバーの登録',description:'My Pioneerで管理します。有効なトークンとユーザーキーがある場合は、上のカードから本体へのログインとチェックインができます。',href:guide,app:true},
  {name:'クルマWi-Fiの契約・チャージ',description:'契約者のアカウントで利用登録や課金を行います。',href:guide,app:true},
 ]},
 {group:'ナビ・音声サービス',items:[
  {name:'目的地検索・ルート設定・予約',description:'My NP1の地図・検索サービスを使用します。',href:guide+'EFA94573B9C47AE2BC43389CFA565039.php',app:true},
  {name:'自宅・勤務地・よく行く場所',description:'My NP1のアカウントに登録します。',href:guide,app:true},
  {name:'渋滞・規制・オービス・ナビ表示',description:'交通情報の取得やスマートフォンの表示を操作します。',href:guide,app:true},
  {name:'CarPlay・Android Auto',description:'対応するスマートフォンと車載画面で操作します。',href:guide,app:true},
  {name:'コエ替えの購入・ダウンロード',description:'購入済み音声の選択は下の「案内・コエ」。購入・取得にはMy NP1を使います。',href:guide,app:true},
  {name:'ドライブトピック・ドライブコール・ガイド',description:'My NP1のアカウントと対応サービスが必要です。',href:guide,app:true},
 ]},
 {group:'本体の管理',items:[
  {name:'本体情報・バージョン',description:'本体・ストレージ・サービスの状態を確認します。',href:'#system'},
  {name:'利用規約・取付手順',description:'純正の利用規約と取付手順を確認します。',href:guide,app:true},
 ]},
]
