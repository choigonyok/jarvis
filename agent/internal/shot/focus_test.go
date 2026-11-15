package shot

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"testing"
)

// page is a screen as a 보안문자 page is drawn: white, a logo over a bordered
// box in the middle, and the page's scrollbar on the right edge.
func page(t *testing.T, fill bool) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1440, 900))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	paint := func(r image.Rectangle, c color.Color) {
		draw.Draw(img, r, image.NewUniform(c), image.Point{}, draw.Src)
	}
	paint(image.Rect(1425, 0, 1440, 900), color.RGBA{180, 180, 180, 255}) // scrollbar
	paint(image.Rect(634, 46, 790, 80), color.RGBA{3, 199, 90, 255})      // logo
	paint(image.Rect(482, 118, 942, 696), color.RGBA{200, 200, 200, 255}) // box border
	paint(image.Rect(484, 120, 940, 694), color.White)
	paint(image.Rect(520, 292, 902, 466), color.RGBA{60, 60, 60, 255}) // the receipt
	if fill {
		paint(image.Rect(0, 0, 1425, 900), color.RGBA{30, 30, 120, 255})
		paint(image.Rect(0, 0, 4, 4), color.White)
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func size(t *testing.T, raw []byte) image.Rectangle {
	t.Helper()
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return img.Bounds()
}

// The box and its logo, without the white around them or the scrollbar.
func TestFocusCutsThePlainMargin(t *testing.T) {
	got, ok, err := Focus(page(t, false), nil)
	if err != nil || !ok {
		t.Fatalf("잘라야 합니다: %v %v", ok, err)
	}
	r := size(t, got)
	// 482..942 × 46..696 with a 16 px margin, give or take the 2 px stride.
	if r.Dx() < 460+32-4 || r.Dx() > 460+32+4 || r.Dy() < 650+32-4 || r.Dy() > 650+32+4 {
		t.Fatalf("상자와 로고만 남아야 합니다: %v", r)
	}
}

// An area given in percent is that part; a screen full of content, or an
// area that is no area, has nothing to cut.
func TestFocusTakesTheAreaGiven(t *testing.T) {
	got, ok, err := Focus(page(t, false), []float64{25, 10, 50, 50})
	if err != nil || !ok {
		t.Fatalf("준 부분을 잘라야 합니다: %v %v", ok, err)
	}
	if r := size(t, got); r.Dx() != 720 || r.Dy() != 450 {
		t.Fatalf("1440×900 의 50%%×50%%: %v", r)
	}
	if _, ok, _ := Focus(page(t, true), nil); ok {
		t.Fatal("화면 전체가 내용이면 자를 것이 없습니다")
	}
	if _, ok, _ := Focus(page(t, false), []float64{10, 10, 0, 50}); ok {
		t.Fatal("너비가 없는 부분은 자르지 않습니다")
	}
	if _, _, err := Focus([]byte("not a picture"), nil); err == nil {
		t.Fatal("사진이 아니면 오류")
	}
}
