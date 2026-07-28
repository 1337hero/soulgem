// Package mathutil contains the load-bearing floating-point operations used by
// the NIF to glTF pipeline. Keep evaluation and accumulation order stable:
// character GLBs are regression-checked byte for byte.
package mathutil

import (
	"math"
	"strconv"
)

type Vec3 [3]float64
type Mat3 [9]float64
type Mat4 [4][4]float64
type Transform struct {
	Translation Vec3
	Rotation    Mat3
	Scale       float64
}

// These tiny barriers prevent the compiler from fusing multiply-add sequences.
// CPython evaluates each source-level multiply and add separately; preserving
// that rounding is required for byte-identical GLB output.
//
//go:noinline
func add(a, b float64) float64 { return a + b }

//go:noinline
func sub(a, b float64) float64 { return a - b }

//go:noinline
func mul(a, b float64) float64 { return a * b }

//go:noinline
func div(a, b float64) float64 { return a / b }

func sum3(a, b, c float64) float64    { return add(add(a, b), c) }
func sum4(a, b, c, d float64) float64 { return add(add(add(a, b), c), d) }

// compensated4 mirrors CPython 3.14's CompensatedSum, which builtin sum()
// applies to the four products in mat_mul.
func compensated4(a, b, c, d float64) float64 {
	hi, lo := 0.0, 0.0
	for _, value := range [4]float64{a, b, c, d} {
		t := add(hi, value)
		if math.Abs(hi) >= math.Abs(value) {
			lo = add(lo, add(sub(hi, t), value))
		} else {
			lo = add(lo, add(sub(value, t), hi))
		}
		hi = t
	}
	if lo != 0 && !math.IsInf(lo, 0) && !math.IsNaN(lo) {
		return add(hi, lo)
	}
	return hi
}

func TransformMatrix(tf Transform) Mat4 {
	t, r, s := tf.Translation, tf.Rotation, tf.Scale
	return Mat4{
		{mul(r[0], s), mul(r[1], s), mul(r[2], s), t[0]},
		{mul(r[3], s), mul(r[4], s), mul(r[5], s), t[1]},
		{mul(r[6], s), mul(r[7], s), mul(r[8], s), t[2]},
		{0, 0, 0, 1},
	}
}

func Multiply(a, b Mat4) (out Mat4) {
	for i := range 4 {
		for j := range 4 {
			out[i][j] = compensated4(
				mul(a[i][0], b[0][j]), mul(a[i][1], b[1][j]),
				mul(a[i][2], b[2][j]), mul(a[i][3], b[3][j]),
			)
		}
	}
	return
}

func TransformPosition(m Mat4, value Vec3) Vec3 {
	x, y, z := value[0], value[1], value[2]
	return Vec3{
		sum4(mul(m[0][0], x), mul(m[0][1], y), mul(m[0][2], z), m[0][3]),
		sum4(mul(m[1][0], x), mul(m[1][1], y), mul(m[1][2], z), m[1][3]),
		sum4(mul(m[2][0], x), mul(m[2][1], y), mul(m[2][2], z), m[2][3]),
	}
}

func TransformDirection(m Mat4, value Vec3) Vec3 {
	x, y, z := value[0], value[1], value[2]
	return Vec3{
		sum3(mul(m[0][0], x), mul(m[0][1], y), mul(m[0][2], z)),
		sum3(mul(m[1][0], x), mul(m[1][1], y), mul(m[1][2], z)),
		sum3(mul(m[2][0], x), mul(m[2][1], y), mul(m[2][2], z)),
	}
}

func QuaternionFromMatrix(r Mat3) [4]float64 {
	m00, m01, m02 := r[0], r[1], r[2]
	m10, m11, m12 := r[3], r[4], r[5]
	m20, m21, m22 := r[6], r[7], r[8]
	var w, x, y, z float64
	tr := m00 + m11 + m22
	if tr > 0 {
		s := math.Sqrt(tr+1.0) * 2
		w, x, y, z = 0.25*s, (m21-m12)/s, (m02-m20)/s, (m10-m01)/s
	} else if m00 > m11 && m00 > m22 {
		s := math.Sqrt(1.0+m00-m11-m22) * 2
		w, x, y, z = (m21-m12)/s, 0.25*s, (m01+m10)/s, (m02+m20)/s
	} else if m11 > m22 {
		s := math.Sqrt(1.0+m11-m00-m22) * 2
		w, x, y, z = (m02-m20)/s, (m01+m10)/s, 0.25*s, (m12+m21)/s
	} else {
		s := math.Sqrt(1.0+m22-m00-m11) * 2
		w, x, y, z = (m10-m01)/s, (m02+m20)/s, (m12+m21)/s, 0.25*s
	}
	return [4]float64{x, y, z, w}
}

func AffineInverse(m Mat4) Mat4 {
	a, b, c := m[0], m[1], m[2]
	det := add(
		sub(
			mul(a[0], sub(mul(b[1], c[2]), mul(b[2], c[1]))),
			mul(a[1], sub(mul(b[0], c[2]), mul(b[2], c[0]))),
		),
		mul(a[2], sub(mul(b[0], c[1]), mul(b[1], c[0]))),
	)
	inv := Mat4{
		{div(sub(mul(b[1], c[2]), mul(b[2], c[1])), det), div(sub(mul(a[2], c[1]), mul(a[1], c[2])), det), div(sub(mul(a[1], b[2]), mul(a[2], b[1])), det), 0},
		{div(sub(mul(b[2], c[0]), mul(b[0], c[2])), det), div(sub(mul(a[0], c[2]), mul(a[2], c[0])), det), div(sub(mul(a[2], b[0]), mul(a[0], b[2])), det), 0},
		{div(sub(mul(b[0], c[1]), mul(b[1], c[0])), det), div(sub(mul(a[1], c[0]), mul(a[0], c[1])), det), div(sub(mul(a[0], b[1]), mul(a[1], b[0])), det), 0},
		{0, 0, 0, 1},
	}
	t := [3]float64{m[0][3], m[1][3], m[2][3]}
	for i := range 3 {
		inv[i][3] = -sum3(mul(inv[i][0], t[0]), mul(inv[i][1], t[1]), mul(inv[i][2], t[2]))
	}
	return inv
}

// Round rounds to a number of decimal places the way Python's round does:
// half-to-even against the value's exact binary expansion. Scaling by a power
// of ten and rounding instead — the obvious implementation — is off by one in
// the last digit whenever the product lands near a tie, because the scaling
// itself is inexact. The asset pipeline was ported from Python and its outputs
// are checked against committed baselines, so that last digit is load-bearing.
func Round(value float64, places int) float64 {
	rounded, err := strconv.ParseFloat(strconv.FormatFloat(value, 'f', places, 64), 64)
	if err != nil {
		return value // Only NaN and infinities, which have no decimal form.
	}
	return rounded
}

func round4(v float64) float64 { return Round(v, 4) }

func SmoothNormals(pos []Vec3, tris [][3]int) []Vec3 {
	type key [3]float64
	weld := make(map[key]int)
	canon := make([]int, len(pos))
	for i, p := range pos {
		k := key{round4(p[0]), round4(p[1]), round4(p[2])}
		j, ok := weld[k]
		if !ok {
			j = i
			weld[k] = i
		}
		canon[i] = j
	}
	acc := make([]Vec3, len(pos))
	for _, tri := range tris {
		a, b, c := pos[tri[0]], pos[tri[1]], pos[tri[2]]
		ux, uy, uz := sub(b[0], a[0]), sub(b[1], a[1]), sub(b[2], a[2])
		vx, vy, vz := sub(c[0], a[0]), sub(c[1], a[1]), sub(c[2], a[2])
		nx, ny, nz := sub(mul(uy, vz), mul(uz, vy)), sub(mul(uz, vx), mul(ux, vz)), sub(mul(ux, vy), mul(uy, vx))
		for _, vi := range tri {
			j := canon[vi]
			acc[j][0] = add(acc[j][0], nx)
			acc[j][1] = add(acc[j][1], ny)
			acc[j][2] = add(acc[j][2], nz)
		}
	}
	out := make([]Vec3, len(pos))
	for i := range pos {
		v := acc[canon[i]]
		l := math.Sqrt(sum3(mul(v[0], v[0]), mul(v[1], v[1]), mul(v[2], v[2])))
		if l == 0 {
			l = 1
		}
		out[i] = Vec3{div(v[0], l), div(v[1], l), div(v[2], l)}
	}
	return out
}

type Influence struct {
	Matrix Mat4
	Weight float64
}

func BakeBindPose(positions, normals []Vec3, influences [][]Influence) ([]Vec3, []Vec3) {
	newPos := append([]Vec3(nil), positions...)
	newNrm := append([]Vec3(nil), normals...)
	for vi, infl := range influences {
		var px, py, pz, nx, ny, nz, total float64
		for _, in := range infl {
			m, w := in.Matrix, in.Weight
			p := TransformPosition(m, positions[vi])
			px = add(px, mul(w, p[0]))
			py = add(py, mul(w, p[1]))
			pz = add(pz, mul(w, p[2]))
			if len(normals) != 0 {
				n := TransformDirection(m, normals[vi])
				nx = add(nx, mul(w, n[0]))
				ny = add(ny, mul(w, n[1]))
				nz = add(nz, mul(w, n[2]))
			}
			total = add(total, w)
		}
		if total > 1e-6 {
			newPos[vi] = Vec3{div(px, total), div(py, total), div(pz, total)}
			if len(normals) != 0 {
				l := math.Sqrt(sum3(mul(nx, nx), mul(ny, ny), mul(nz, nz)))
				if l > 1e-6 {
					newNrm[vi] = Vec3{div(nx, l), div(ny, l), div(nz, l)}
				}
			}
		}
	}
	return newPos, newNrm
}
