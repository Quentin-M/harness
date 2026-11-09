"""Faster, autograd-friendly gated-delta chunk scan for training (chunk 16, product-form inverse)."""
import functools
import mlx.core as mx
import mlx_vlm.models.qwen3_5.gated_delta as gd

CHUNK = 16


def _inv(Tmat, C):
    # (I + A)^-1 for strictly-lower A (nilpotent, A^C = 0) = (I - A)(I + A^2)(I + A^4)...
    A = Tmat - mx.eye(C, dtype=Tmat.dtype)
    I = mx.eye(C, dtype=Tmat.dtype)
    out = I - A
    P = A
    n = 1
    while n * 2 < C:
        P = P @ P
        out = out @ (I + P)
        n *= 2
    return out


def install(chunk=CHUNK):
    orig = gd.gated_delta_chunked
    gd._invert_unit_lower = _inv
    gd.gated_delta_chunked = lambda q, k, v, g, beta, state, mask=None, C=chunk: orig(q, k, v, g, beta, state, mask, C)


def make_qlin(group_size=16, bits=4, mode="nvfp4"):
    @mx.custom_function
    def qlin(x, w, s):
        return mx.quantized_matmul(x, w, s, None, transpose=True, group_size=group_size, bits=bits, mode=mode)

    @qlin.vjp
    def qlin_vjp(primals, cotangent, output):
        x, w, s = primals
        wd = mx.dequantize(w, s, None, group_size, bits, mode=mode)
        return cotangent @ wd.astype(cotangent.dtype), mx.zeros_like(w), mx.zeros_like(s)

    return qlin
