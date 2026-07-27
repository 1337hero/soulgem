"""Shared stdlib math for the NIF -> glTF pipeline. Operation order is load-bearing:
GLB output is verified byte-identical, so don't "improve" the arithmetic."""


def tf_mat(tf):
    """(translation, row-major 3x3, scale) -> 4x4 affine (row-major nested lists)."""
    t, r9, s = tf
    return [[r9[0]*s, r9[1]*s, r9[2]*s, t[0]],
            [r9[3]*s, r9[4]*s, r9[5]*s, t[1]],
            [r9[6]*s, r9[7]*s, r9[8]*s, t[2]],
            [0, 0, 0, 1]]


def mat_mul(a, b):
    return [[sum(a[i][k]*b[k][j] for k in range(4)) for j in range(4)] for i in range(4)]


def quat_from_mat3(r):
    """Row-major 3x3 -> glTF quaternion [x,y,z,w]."""
    m00, m01, m02, m10, m11, m12, m20, m21, m22 = r
    tr = m00 + m11 + m22
    if tr > 0:
        s = (tr + 1.0) ** 0.5 * 2
        w, x, y, z = 0.25 * s, (m21 - m12) / s, (m02 - m20) / s, (m10 - m01) / s
    elif m00 > m11 and m00 > m22:
        s = (1.0 + m00 - m11 - m22) ** 0.5 * 2
        w, x, y, z = (m21 - m12) / s, 0.25 * s, (m01 + m10) / s, (m02 + m20) / s
    elif m11 > m22:
        s = (1.0 + m11 - m00 - m22) ** 0.5 * 2
        w, x, y, z = (m02 - m20) / s, (m01 + m10) / s, 0.25 * s, (m12 + m21) / s
    else:
        s = (1.0 + m22 - m00 - m11) ** 0.5 * 2
        w, x, y, z = (m10 - m01) / s, (m02 + m20) / s, (m12 + m21) / s, 0.25 * s
    return [x, y, z, w]


def affine_inverse(m):
    """Inverse of a 4x4 affine matrix (row-major nested lists)."""
    a, b, c = m[0][:3], m[1][:3], m[2][:3]
    det = (a[0]*(b[1]*c[2]-b[2]*c[1]) - a[1]*(b[0]*c[2]-b[2]*c[0]) + a[2]*(b[0]*c[1]-b[1]*c[0]))
    inv3 = [
        [(b[1]*c[2]-b[2]*c[1])/det, (a[2]*c[1]-a[1]*c[2])/det, (a[1]*b[2]-a[2]*b[1])/det],
        [(b[2]*c[0]-b[0]*c[2])/det, (a[0]*c[2]-a[2]*c[0])/det, (a[2]*b[0]-a[0]*b[2])/det],
        [(b[0]*c[1]-b[1]*c[0])/det, (a[1]*c[0]-a[0]*c[1])/det, (a[0]*b[1]-a[1]*b[0])/det],
    ]
    t = [m[0][3], m[1][3], m[2][3]]
    ti = [-(inv3[i][0]*t[0] + inv3[i][1]*t[1] + inv3[i][2]*t[2]) for i in range(3)]
    return [inv3[0] + [ti[0]], inv3[1] + [ti[1]], inv3[2] + [ti[2]], [0, 0, 0, 1]]


def smooth_normals(pos, tris):
    """Area-weighted smooth normals, welding duplicate positions (UV seams)."""
    weld = {}
    canon = []
    for i, p in enumerate(pos):
        key = (round(p[0], 4), round(p[1], 4), round(p[2], 4))
        canon.append(weld.setdefault(key, i))
    acc = [[0.0, 0.0, 0.0] for _ in pos]
    for a, b, c in tris:
        ax, ay, az = pos[a]; bx, by, bz = pos[b]; cx, cy, cz = pos[c]
        ux, uy, uz = bx-ax, by-ay, bz-az
        vx, vy, vz = cx-ax, cy-ay, cz-az
        nx, ny, nz = uy*vz-uz*vy, uz*vx-ux*vz, ux*vy-uy*vx  # length = 2*area
        for i in (a, b, c):
            j = canon[i]
            acc[j][0] += nx; acc[j][1] += ny; acc[j][2] += nz
    out = []
    for i in range(len(pos)):
        x, y, z = acc[canon[i]]
        l = (x*x + y*y + z*z) ** 0.5 or 1.0
        out.append((x/l, y/l, z/l))
    return out


def bake_bind_pose(positions, normals, influences):
    """Weighted bind-pose bake: influences[vi] = [(bone_to_global 4x4, weight), ...].

    Returns (new_positions, new_normals). Accumulation follows the caller's
    influence order — keep that order stable, float sums are order-sensitive.
    """
    new_pos = list(positions)
    new_nrm = list(normals)
    for vi, infl in enumerate(influences):
        x, y, z = positions[vi]
        px = py = pz = 0.0
        nx = ny = nz = 0.0
        tw = 0.0
        for m, w in infl:
            px += w * (m[0][0]*x + m[0][1]*y + m[0][2]*z + m[0][3])
            py += w * (m[1][0]*x + m[1][1]*y + m[1][2]*z + m[1][3])
            pz += w * (m[2][0]*x + m[2][1]*y + m[2][2]*z + m[2][3])
            if normals:
                a, b, c = normals[vi]
                nx += w * (m[0][0]*a + m[0][1]*b + m[0][2]*c)
                ny += w * (m[1][0]*a + m[1][1]*b + m[1][2]*c)
                nz += w * (m[2][0]*a + m[2][1]*b + m[2][2]*c)
            tw += w
        if tw > 1e-6:
            new_pos[vi] = (px/tw, py/tw, pz/tw)
            if normals:
                l = (nx*nx + ny*ny + nz*nz) ** 0.5
                if l > 1e-6:
                    new_nrm[vi] = (nx/l, ny/l, nz/l)
    return new_pos, new_nrm


def _demo():
    ident = [[1, 0, 0, 0], [0, 1, 0, 0], [0, 0, 1, 0], [0, 0, 0, 1]]
    assert mat_mul(ident, ident) == ident
    m = tf_mat(((1, 2, 3), (0, -1, 0, 1, 0, 0, 0, 0, 1), 2.0))
    inv = affine_inverse(m)
    back = mat_mul(m, inv)
    assert all(abs(back[i][j] - ident[i][j]) < 1e-9 for i in range(4) for j in range(4)), back
    assert quat_from_mat3((1, 0, 0, 0, 1, 0, 0, 0, 1)) == [0.0, 0.0, 0.0, 1.0]
    n = smooth_normals([(0, 0, 0), (1, 0, 0), (0, 1, 0)], [(0, 1, 2)])
    assert all(abs(v[2] - 1.0) < 1e-9 for v in n), n
    # two half-weight influences at the same translation == that translation
    shift = tf_mat(((10, 0, 0), (1, 0, 0, 0, 1, 0, 0, 0, 1), 1.0))
    p, nr = bake_bind_pose([(1, 0, 0)], [(0, 0, 1)], [[(shift, 0.5), (shift, 0.5)]])
    assert p[0] == (11.0, 0.0, 0.0), p
    assert nr[0] == (0.0, 0.0, 1.0), nr
    print('mathutil ok')


if __name__ == '__main__':
    _demo()
