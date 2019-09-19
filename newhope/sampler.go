package newhope

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"math"
)

// KYSampler is the structure holding the parameters for the gaussian sampling.
type KYSampler struct {
	context *Context
	sigma   float64
	bound   int
	Matrix  [][]uint8
}

// NewKYSampler creates a new KYSampler with sigma and bound that will be used to sample polynomial within the provided discret gaussian distribution.
func (context *Context) NewKYSampler(sigma float64, bound int) *KYSampler {
	kysampler := new(KYSampler)
	kysampler.context = context
	kysampler.sigma = sigma
	kysampler.bound = bound
	kysampler.Matrix = computeMatrix(sigma, bound)
	return kysampler
}

//gaussian computes (1/variange*sqrt(pi)) * exp((x^2) / (2*variance^2)),  2.50662827463100050241576528481104525300698674060993831662992357 = sqrt(2*pi)
func gaussian(x, sigma float64) float64 {
	return (1 / (sigma * 2.5066282746310007)) * math.Exp(-((math.Pow(x, 2)) / (2 * sigma * sigma)))
}

// computeMatrix computes the binary expension with precision x in bits of the normal distribution
// with sigma and bound. Returns a matrix of the form M = [[0,1,0,0,...],[0,0,1,,0,...]],
// where each row is the binary expension of the normal distribution of index(row) with sigma and bound (center=0).
func computeMatrix(sigma float64, bound int) [][]uint8 {
	var g float64
	var x uint64

	precision := uint64(56)

	M := make([][]uint8, bound)

	breakCounter := 0

	for i := 0; i < bound; i++ {

		g = gaussian(float64(i), sigma)

		if i == 0 {
			g *= math.Exp2(float64(precision) - 1)
		} else {
			g *= math.Exp2(float64(precision))
		}

		x = uint64(g)

		if x == 0 {
			break
		}

		M[i] = make([]uint8, precision-1)

		for j := uint64(0); j < precision-1; j++ {
			M[i][j] = uint8((x >> (precision - j - 2)) & 1)
		}

		breakCounter += 1
	}

	M = M[:breakCounter]

	return M
}

func kysampling(M [][]uint8, randomBytes []byte, pointer uint8) (uint32, uint32, []byte, uint8) {

	var sign uint8

	d := 0
	col := 0
	colLen := len(M)

	for {

		// Uses one random byte per cycle and cycle through the randombytes
		for i := pointer; i < 8; i++ {

			d = (d << 1) + 1 - int((uint8(randomBytes[0])>>i)&1)

			// There is small probability that it will get out of the bound, then
			// rerun until it gets a proper output
			if d > colLen-1 {
				return kysampling(M, randomBytes, i)
			}

			for row := colLen - 1; row >= 0; row-- {

				d -= int(M[row][col])

				if d == -1 {

					// Sign
					if i == 7 {
						pointer = 0
						// If the last bit of the byte was read, samples a new byte for the sign
						randomBytes = randomBytes[1:]

						if len(randomBytes) == 0 {
							randomBytes = make([]byte, 8)
							if _, err := rand.Read(randomBytes); err != nil {
								panic("crypto rand error")
							}
						}

						sign = uint8(randomBytes[0]) & 1

					} else {
						pointer = i
						// Else the sign is the next bit of the byte
						sign = uint8(randomBytes[0]>>(i+1)) & 1
					}

					return uint32(row), uint32(sign), randomBytes, pointer + 1
				}
			}

			col += 1
		}

		// Resets the bit pointer and discards the used byte
		pointer = 0
		randomBytes = randomBytes[1:]

		// Sample 8 new bytes if the last byte was discarded
		if len(randomBytes) == 0 {
			randomBytes = make([]byte, 8)
			if _, err := rand.Read(randomBytes); err != nil {
				panic("crypto rand error")
			}
		}

	}
}

// SampleNew samples a new polynomial with gaussian distribution given the target kys parameters.
func (kys *KYSampler) SampleNew() *Poly {
	Pol := kys.context.NewPoly()
	kys.Sample(Pol)
	return Pol
}

// SampleNew samples on the target polynomial coefficients with gaussian distribution given the target kys parameters.
func (kys *KYSampler) Sample(Pol *Poly) {

	var coeff uint32
	var sign uint32

	randomBytes := make([]byte, 8)
	pointer := uint8(0)

	if _, err := rand.Read(randomBytes); err != nil {
		panic("crypto rand error")
	}

	for i := uint32(0); i < kys.context.N; i++ {

		coeff, sign, randomBytes, pointer = kysampling(kys.Matrix, randomBytes, pointer)

		for j, qi := range kys.context.Modulus {
			Pol.Coeffs[j][i] = (coeff & (sign * 0xFFFFFFFF)) | ((qi - coeff) & ((sign ^ 1) * 0xFFFFFFFF))

		}
	}
}

// SampleNTTNew samples a polynomial with gaussian distribution given the target kys context and apply the NTT.
func (kys *KYSampler) SampleNTTNew() *Poly {
	Pol := kys.SampleNew()
	kys.context.NTT(Pol, Pol)
	return Pol
}

// SampleNew samples on the target polynomial coefficients with gaussian distribution given the target kys parameters,and applies the NTT.
func (kys *KYSampler) SampleNTT(Pol *Poly) {
	kys.Sample(Pol)
	kys.context.NTT(Pol, Pol)
}

// TernarySampler is the structure holding the parameters for sampling polynomials of the form [-1, 0, 1].
type TernarySampler struct {
	context          *Context
	Matrix           [][]uint32
	MatrixMontgomery [][]uint32

	KYMatrix [][]uint8
}

// NewTernarySampler creates a new TernarySampler from the target context.
func (context *Context) NewTernarySampler() *TernarySampler {

	sampler := new(TernarySampler)
	sampler.context = context

	sampler.Matrix = make([][]uint32, len(context.Modulus))
	sampler.MatrixMontgomery = make([][]uint32, len(context.Modulus))

	for i, Qi := range context.Modulus {

		sampler.Matrix[i] = make([]uint32, 3)
		sampler.Matrix[i][0] = 0
		sampler.Matrix[i][1] = 1
		sampler.Matrix[i][2] = Qi - 1

		sampler.MatrixMontgomery[i] = make([]uint32, 3)
		sampler.MatrixMontgomery[i][0] = 0
		sampler.MatrixMontgomery[i][1] = MForm(1, Qi, context.bredParams[i])
		sampler.MatrixMontgomery[i][2] = MForm(Qi-1, Qi, context.bredParams[i])
	}

	return sampler
}

func computeMatrixTernary(p float64) (M [][]uint8) {
	var g float64
	var x uint64

	precision := uint64(56)

	M = make([][]uint8, 2)

	g = 1 - p
	g *= math.Exp2(float64(precision))
	x = uint64(g)

	M[0] = make([]uint8, precision-1)
	for j := uint64(0); j < precision-1; j++ {
		M[0][j] = uint8((x >> (precision - j - 1)) & 1)
	}

	g = p
	g *= math.Exp2(float64(precision))
	x = uint64(g)

	M[1] = make([]uint8, precision-1)
	for j := uint64(0); j < precision-1; j++ {
		M[1][j] = uint8((x >> (precision - j - 1)) & 1)
	}

	return M
}

// SampleMontgomeryNew samples coefficients with ternary distribution in montgomery form on the target polynomial.
func sample(context *Context, samplerMatrix [][]uint32, p float64, pol *Poly) (err error) {

	if p == 0 {
		return errors.New("cannot sample -> p = 0")
	}

	var coeff uint32
	var sign uint32

	if p == 0.5 {

		randomBytesCoeffs := make([]byte, context.N>>3)
		randomBytesSign := make([]byte, context.N>>3)

		if _, err := rand.Read(randomBytesCoeffs); err != nil {
			panic("crypto rand error")
		}

		if _, err := rand.Read(randomBytesSign); err != nil {
			panic("crypto rand error")
		}

		for i := uint32(0); i < context.N; i++ {
			coeff = uint32(uint8(randomBytesCoeffs[i>>3])>>(i&7)) & 1
			sign = uint32(uint8(randomBytesSign[i>>3])>>(i&7)) & 1

			for j := range context.Modulus {
				pol.Coeffs[j][i] = samplerMatrix[j][(coeff&(sign^1))|((sign&coeff)<<1)] //(coeff & (sign^1)) | (qi - 1) * (sign & coeff)
			}
		}

	} else {

		matrix := computeMatrixTernary(p)

		randomBytes := make([]byte, 8)

		pointer := uint8(0)

		if _, err := rand.Read(randomBytes); err != nil {
			panic("crypto rand error")
		}

		for i := uint32(0); i < context.N; i++ {

			coeff, sign, randomBytes, pointer = kysampling(matrix, randomBytes, pointer)

			for j := range context.Modulus {
				pol.Coeffs[j][i] = samplerMatrix[j][(coeff&(sign^1))|((sign&coeff)<<1)] //(coeff & (sign^1)) | (qi - 1) * (sign & coeff)
			}
		}
	}

	return nil
}

func (sampler *TernarySampler) Sample(p float64, pol *Poly) (err error) {
	if err = sample(sampler.context, sampler.Matrix, p, pol); err != nil {
		return err
	}
	return nil
}

func (sampler *TernarySampler) SampleMontgomery(p float64, pol *Poly) (err error) {
	if err = sample(sampler.context, sampler.MatrixMontgomery, p, pol); err != nil {
		return err
	}
	return nil
}

// SampleNew samples a new polynomial with ternary distribution.
func (sampler *TernarySampler) SampleNew(p float64) (pol *Poly, err error) {
	pol = sampler.context.NewPoly()
	if err = sampler.Sample(p, pol); err != nil {
		return nil, err
	}
	return pol, nil
}

// SampleMontgomeryNew samples a new polynomial with ternary distribution in montgomery form.
func (sampler *TernarySampler) SampleMontgomeryNew(p float64) (pol *Poly, err error) {
	pol = sampler.context.NewPoly()
	if err = sampler.SampleMontgomery(p, pol); err != nil {
		return nil, err
	}
	return pol, nil
}

// SampleNTTNew samples a new polynomial with ternary distribution in the NTT domain.
func (sampler *TernarySampler) SampleNTTNew(p float64) (pol *Poly, err error) {
	pol = sampler.context.NewPoly()
	if err = sampler.Sample(p, pol); err != nil {
		return nil, err
	}
	sampler.context.NTT(pol, pol)
	return pol, nil
}

// SampleNTT samples coefficients with ternary distribution in the NTT domain on the target polynomial.
func (sampler *TernarySampler) SampleNTT(p float64, pol *Poly) (err error) {
	if err = sampler.Sample(p, pol); err != nil {
		return err
	}
	sampler.context.NTT(pol, pol)

	return nil
}

// SampleNTTNew samples a new polynomial with ternary distribution in the NTT domain and in montgomery form.
func (sampler *TernarySampler) SampleMontgomeryNTTNew(p float64) (pol *Poly, err error) {
	if pol, err = sampler.SampleMontgomeryNew(p); err != nil {
		return nil, err
	}
	sampler.context.NTT(pol, pol)
	return pol, nil
}

// SampleNTT samples coefficients with ternary distribution in the NTT domain and in montgomery form on the target polynomial.
func (sampler *TernarySampler) SampleMontgomeryNTT(p float64, pol *Poly) (err error) {
	if err = sampler.SampleMontgomery(p, pol); err != nil {
		return err
	}
	sampler.context.NTT(pol, pol)
	return nil
}

// RandUniform samples a uniform randomInt variable in the range [0, mask] until randomInt is in the range [0, v-1].
// mask needs to be of the form 2^n -1.
func RandUniform(v uint64, mask uint64) (randomInt uint64) {
	for {
		randomInt = randInt64(mask)
		if randomInt < v {
			return randomInt
		}
	}
}

func RandUniform32(v uint32, mask uint32) (randomInt uint32) {
	for {
		randomInt = randInt32(mask)
		if randomInt < v {
			return randomInt
		}
	}
}

// randInt3 samples a bit and a sign with rejection sampling (25% chance of failure), with probabilities :
// Pr[int = 0 : 1/3 ; int = 1 : 2/3]
// Pr[sign = 1 : 1/2; sign = 0 : 1/2]
func randInt3() (uint64, uint64) {
	var randomInt uint64

	for {
		randomInt = randInt8()
		if (randomInt & 3) < 3 {
			// (a|b) is 1/3 = 0 and 2/3 = 1 if (a<<1 | b) in [0,1,2]
			return ((randomInt >> 1) | randomInt) & 1, (randomInt >> 2) & 1
		}
	}
}

// randUint3 samples a uniform variable in the range [0, 2]
func randUint3() uint64 {
	var randomInt uint64
	for {
		randomInt = randInt8() & 3
		if randomInt < 3 {
			return randomInt
		}
	}
}

// randInt8 samples a uniform variable in the range [0, 255]
func randInt8() uint64 {

	// generate random 4 bytes
	randomBytes := make([]byte, 1)
	if _, err := rand.Read(randomBytes); err != nil {
		panic("crypto rand error")
	}
	// return required bits
	return uint64(randomBytes[0])
}

// randInt32 samples a uniform variable in the range [0, mask], where mask is of the form 2^n-1, with n in [0, 32].
func randInt32(mask uint32) uint32 {

	// generate random 4 bytes
	randomBytes := make([]byte, 4)
	_, err := rand.Read(randomBytes)
	if err != nil {
		panic("crypto rand error")
	}

	// return required bits
	return mask & binary.BigEndian.Uint32(randomBytes)
}

// randInt64 samples a uniform variable in the range [0, mask], where mask is of the form 2^n-1, with n in [0, 64].
func randInt64(mask uint64) uint64 {

	// generate random 8 bytes
	randomBytes := make([]byte, 8)
	_, err := rand.Read(randomBytes)
	if err != nil {
		panic("crypto rand error")
	}

	// return required bits
	return mask & binary.BigEndian.Uint64(randomBytes)
}
