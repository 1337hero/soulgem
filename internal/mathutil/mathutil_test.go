package mathutil

import (
	"math"
	"testing"
)

func TestPrimitives(t *testing.T) {
	ident := Mat4{{1, 0, 0, 0}, {0, 1, 0, 0}, {0, 0, 1, 0}, {0, 0, 0, 1}}
	if got := Multiply(ident, ident); got != ident {
		t.Fatalf("identity multiply = %#v", got)
	}
	m := TransformMatrix(Transform{
		Translation: Vec3{1, 2, 3},
		Rotation:    Mat3{0, -1, 0, 1, 0, 0, 0, 0, 1},
		Scale:       2,
	})
	back := Multiply(m, AffineInverse(m))
	for i := range 4 {
		for j := range 4 {
			if math.Abs(back[i][j]-ident[i][j]) >= 1e-9 {
				t.Fatalf("inverse product = %#v", back)
			}
		}
	}
	if got := QuaternionFromMatrix(Mat3{1, 0, 0, 0, 1, 0, 0, 0, 1}); got != [4]float64{0, 0, 0, 1} {
		t.Fatalf("identity quaternion = %#v", got)
	}
	n := SmoothNormals([]Vec3{{0, 0, 0}, {1, 0, 0}, {0, 1, 0}}, [][3]int{{0, 1, 2}})
	for _, v := range n {
		if math.Abs(v[2]-1) >= 1e-9 {
			t.Fatalf("normal = %#v", v)
		}
	}
	shift := TransformMatrix(Transform{
		Translation: Vec3{10, 0, 0},
		Rotation:    Mat3{1, 0, 0, 0, 1, 0, 0, 0, 1},
		Scale:       1,
	})
	p, nr := BakeBindPose(
		[]Vec3{{1, 0, 0}}, []Vec3{{0, 0, 1}},
		[][]Influence{{{Matrix: shift, Weight: .5}, {Matrix: shift, Weight: .5}}},
	)
	if p[0] != (Vec3{11, 0, 0}) || nr[0] != (Vec3{0, 0, 1}) {
		t.Fatalf("bind bake = %#v %#v", p, nr)
	}
}
