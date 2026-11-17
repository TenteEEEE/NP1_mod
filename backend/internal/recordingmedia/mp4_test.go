package recordingmedia

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestMetadataVersion1(t *testing.T) {
	mvhd := make([]byte, 32)
	mvhd[0] = 1
	binary.BigEndian.PutUint32(mvhd[20:], 1000)
	binary.BigEndian.PutUint64(mvhd[24:], 90000)
	v, e := mp4Duration(bytes.NewReader(mvhd), mp4Box{payload: 0, end: int64(len(mvhd))})
	if e != nil || v != 90 {
		t.Fatalf("v1 duration %v %v", v, e)
	}
	binary.BigEndian.PutUint32(mvhd[20:], 0)
	if _, e = mp4Duration(bytes.NewReader(mvhd), mp4Box{payload: 0, end: int64(len(mvhd))}); e == nil {
		t.Fatal("zero timescale accepted")
	}
}

func TestVideoBitrateUsesOnlyVideoSamplesAndMediaDuration(t *testing.T) {
	movie, media := make([]byte, 20), make([]byte, 20)
	binary.BigEndian.PutUint32(movie[12:], 1000)
	binary.BigEndian.PutUint32(movie[16:], 5000)
	binary.BigEndian.PutUint32(media[12:], 1000)
	binary.BigEndian.PutUint32(media[16:], 2000)
	sizes := make([]byte, 20)
	binary.BigEndian.PutUint32(sizes[8:], 2)
	binary.BigEndian.PutUint32(sizes[12:], 100)
	binary.BigEndian.PutUint32(sizes[16:], 300)
	handler, video := make([]byte, 12), make([]byte, 36)
	copy(handler[8:], "vide")
	binary.BigEndian.PutUint32(video, uint32(len(video)))
	copy(video[4:], "avc1")
	binary.BigEndian.PutUint16(video[32:], 640)
	binary.BigEndian.PutUint16(video[34:], 360)
	stsd := originalBox("stsd", []byte{0, 0, 0, 0, 0, 0, 0, 1}, video)
	for _, sampleSizes := range [][]byte{sizes, sizes[:16]} {
		stbl := originalBox("stbl", stsd, originalBox("stsz", sampleSizes))
		track := originalBox("trak", originalBox("mdia", originalBox("mdhd", media), originalBox("minf", stbl), originalBox("hdlr", handler)))
		mp4 := bytes.Join([][]byte{originalBox("ftyp", []byte("isom0000")), originalBox("moov", originalBox("mvhd", movie), track), originalBox("mdat", make([]byte, 1500))}, nil)
		meta, err := ReadMetadata(bytes.NewReader(mp4), int64(len(mp4)))
		var want uint64
		if len(sampleSizes) == len(sizes) {
			want = 1600 // (100 + 300) bytes * 8 / 2 seconds
		}
		if err != nil || meta.VideoBitrate != want {
			t.Fatalf("bitrate = %d, want %d: %v", meta.VideoBitrate, want, err)
		}
	}
}
