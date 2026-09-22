// Package erfx 提供本服务自行实现的误差函数 erf 与余误差函数 erfc。
//
// 一维 Stefan 超越方程里只出现 erf。这个包刻意把两个符号相近的函数
// 分别命名实现并各自测试，防止把 erf 误写成 erfc 导致求根飞掉：
//
//	erf(x)  = (2/√π) ∫_0^x exp(-t²) dt
//	erfc(x) = 1 - erf(x)
//
// 实现策略：
//   - |x| <= 2：直接对收敛极快的幂级数求和
//     erf(x) = (2/√π) Σ (-1)^k x^(2k+1) / (k!(2k+1))
//   - |x| >  2：通过 Laplace 连分式求 erfc 再换算，
//     erfc(x) = (2/√π) e^{-x²} / (2x + 2/(2x + 4/(2x + 6/(2x + …))))
//     该连分式在 x 较大时迅速收敛，200 项可达机器精度。
package erfx

import "math"

// cfTerms 是连分式自底向上递推时保留的项数。
const cfTerms = 200

// Erf 返回误差函数 erf(x)。
func Erf(x float64) float64 {
	if math.IsNaN(x) {
		return math.NaN()
	}
	if x >= 0 {
		return erfPos(x)
	}
	return -erfPos(-x)
}

// Erfc 返回余误差函数 erfc(x) = 1 - erf(x)。
// 单独实现并对外暴露，测试可据此钉死两者差异，防止符号混用。
func Erfc(x float64) float64 {
	if math.IsNaN(x) {
		return math.NaN()
	}
	if math.IsInf(x, 1) {
		return 0
	}
	if math.IsInf(x, -1) {
		return 2
	}
	return 1 - Erf(x)
}

// erfPos 计算 x >= 0 时的 erf(x)。
func erfPos(x float64) float64 {
	if x == 0 {
		return 0
	}
	// x 极大时 erf 恒为 1，避免级数项在浮点范围内无意义地缩放。
	if x > 6 {
		return 1
	}
	if x <= 2 {
		return erfSeries(x)
	}
	return 1 - erfcContinuedFraction(x)
}

// erfSeries 用幂级数求 erf(x)，|x| <= 2 时精度可达机器精度量级。
// 递推项：t_0 = x，t_k = -t_{k-1} * x² * (2k-1) / (k*(2k+1))。
func erfSeries(x float64) float64 {
	x2 := x * x
	term := x
	sum := term
	for k := 1; k < 100; k++ {
		term *= -x2 * float64(2*k-1) / (float64(k) * float64(2*k+1))
		sum += term
		if math.Abs(term) < math.Abs(sum)*1e-17 {
			break
		}
	}
	return 2 / math.SqrtPi * sum
}

// erfcContinuedFraction 用 Laplace 连分式求 erfc(x)，适用于 x > 2。
//
// 自底向上递推分母：
//
//	D_n = 2x
//	D_k = 2x + 2(k+1)/D_{k+1}     (k = n-1 … 0)
//	erfc(x) = (2/√π) e^{-x²} / D_0
func erfcContinuedFraction(x float64) float64 {
	d := 2 * x
	for k := cfTerms - 1; k >= 1; k-- {
		d = 2*x + float64(2*k)/d
	}
	return 2 / math.SqrtPi * math.Exp(-x*x) / d
}
