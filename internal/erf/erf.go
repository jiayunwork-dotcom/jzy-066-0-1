// Package erf 提供自行实现的误差函数与余误差函数。
//
// 二者只差一个常数，却绝不能混用：
//
//	Erf(x)  = (2/sqrt(pi)) * integral_0^x exp(-t^2) dt   // 误差函数
//	Erfc(x) = 1 - Erf(x)                                  // 余误差函数
//
// Stefan 超越方程  sqrt(pi)*lambda*exp(lambda^2)*erf(lambda) = Ste
// 中必须使用 Erf；若误写成 Erfc，求根会立即发散（见 stefan 包的守卫测试）。
//
// 实现采用可靠的经典方案：
//   - |x| <= 2 ：幂级数  erf(x) = 2/sqrt(pi) * sum (-1)^n x^(2n+1)/(n!(2n+1))
//   - |x| >  2 ：erfc 的连分式  erfc(x) = e^(-x^2)/sqrt(pi) /
//     ( x + (1/2)/( x + 1/( x + (3/2)/( x + 2/(...)))))
//     用数值配方（Numerical Recipes）的改良 Lentz 算法自底向上求值，
//     再由 erf = 1 - erfc 还原。
package erf

import "math"

const (
	// tiny 用于 Lentz 算法中防止零除（f0 = b0 = 0 的情形）。
	tiny = 1e-300
	// cfEpsilon 为连分式的收敛判据（相邻增量的相对变化）。
	cfEpsilon = 1e-16
	// cfMaxIter 为连分式的最大项数。
	cfMaxIter = 1000
)

// Erf 返回误差函数 erf(x)。
func Erf(x float64) float64 {
	if math.IsNaN(x) {
		return math.NaN()
	}
	if x == 0 {
		return 0
	}
	if x < 0 {
		return -Erf(-x) // erf(-x) = -erf(x)
	}
	if x <= 2 {
		return erfSeries(x)
	}
	return 1 - erfcContinuedFraction(x)
}

// Erfc 返回余误差函数 erfc(x) = 1 - erf(x)。
func Erfc(x float64) float64 {
	if math.IsNaN(x) {
		return math.NaN()
	}
	if x < 0 {
		return 2 - Erfc(-x) // erfc(-x) = 2 - erfc(x)
	}
	if x <= 2 {
		return 1 - erfSeries(x)
	}
	return erfcContinuedFraction(x)
}

// erfSeries 用交错幂级数计算 erf(x)，要求 x >= 0。
//
//	erf(x) = (2/sqrt(pi)) * x * sum_n (-x^2)^n / (n!(2n+1))
//
// 项与项之间递推：term_n = term_{n-1} * (-x^2)/n。
func erfSeries(x float64) float64 {
	x2 := x * x
	term := 1.0 // n=0 项（括号内级数首项为 1）
	sum := 1.0
	for n := 1; n < 300; n++ {
		term *= -x2 / float64(n)
		sum += term / float64(2*n+1)
		if math.Abs(term)/float64(2*n+1) <= 1e-17*(1+math.Abs(sum)) {
			break
		}
	}
	return 2 / math.SqrtPi * x * sum
}

// erfcContinuedFraction 计算 erfc(x) 的连分式部分，要求 x > 0：
//
//	erfc(x) = exp(-x^2)/sqrt(pi) * F
//	F = 1 / g
//	g = x + a1/(x + a2/(x + a3/(...))),  a_n = n/2
//
// 直接对 g 做标准改良 Lentz 递推（b0 = x，b_n = x，n>=1），
// 最后取倒数。注意不能把整体改写成 b0 = 0 的形式：
// 那会改变分子序列的相位，在第二步就制造出 delta=1 的假收敛。
func erfcContinuedFraction(x float64) float64 {
	// f0 = b0 = x；D0 = 0，C0 = f0。
	d := 0.0
	c := x
	h := x

	for n := 1; n <= cfMaxIter; n++ {
		a := float64(n) / 2.0
		b := x

		d = b + a*d
		if math.Abs(d) < tiny {
			d = tiny
		}
		c = b + a/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		d = 1 / d
		delta := c * d
		h *= delta
		if n >= 2 && math.Abs(delta-1) < cfEpsilon {
			break
		}
	}
	return math.Exp(-x*x) / math.SqrtPi / h
}
