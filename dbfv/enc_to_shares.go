//Encryption-to-shares is the protocol described in Section IV.E.1 of the paper.
//It separates parties P_2,...,P_n (called slaves in this file) from party P_1 (called master).
//The slaves behave similarly to the CKS protocol: the difference is that they sample M_i from Z_t^n
//(which becomes their additive share of the plaintext), and subtract delta*M_i from their decryption share.
//The master behaves differently: it collects all the decryption shares of the slaves, sums them, and performs a
//BFV-decryption with its own secret key share (as described in the paper) to get its additive share
//(it does not produce a decryption share).

package dbfv

import (
	"github.com/ldsec/lattigo/bfv"
	"github.com/ldsec/lattigo/ring"
	"sync"
)

//E2SProtocol contains all the parameters needed to perform the various steps of the protocol.
type E2SProtocol struct {
	cks     *CKSProtocol //CKSProtocol is not embedded to have control over the exposed methods
	encoder bfv.Encoder

	//Just memory pools
	plain  *bfv.Plaintext
	scaler *ring.SimpleScaler
	deltaM *ring.Poly
	cipher *bfv.Ciphertext
	poly   *ring.Poly
}

//E2SDecryptionShare represents the decryption share produced by slaves, which needs to be disclosed and
//collected by the master. It is an element of R_q.
type E2SDecryptionShare struct {
	CKSShare
}

// TODO: when is a MarshalBinary needed?
// UnmarshalBinary decodes a previously marshaled share on the target share.
func (share *E2SDecryptionShare) UnmarshalBinary(data []byte) error {
	return (&share.CKSShare).UnmarshalBinary(data)
}

//AdditiveShare represents the additive share of the plaintext the party possesses after running the protocol.
//The additive shares are elements of Z_t^n, and add up to the original clear vector, not to its plaintext-encoding.
type AdditiveShare struct {
	elem *ring.Poly
}

//NewE2SProtocol allocates a protocol struct
func NewE2SProtocol(params *bfv.Parameters, sigmaSmudging float64) *E2SProtocol {
	cks := NewCKSProtocol(params, sigmaSmudging)

	return &E2SProtocol{cks,
		bfv.NewEncoder(params),
		bfv.NewPlaintext(params),
		ring.NewSimpleScaler(cks.context.params.T, cks.context.contextQ),
		cks.context.contextQ.NewPoly(),
		bfv.NewCiphertext(params, 1),
		cks.context.contextQ.NewPoly()}
}

// AllocateShares allocates both shares: they are both needed by both leaves and slaves.
func (e2s *E2SProtocol) AllocateShares() (*E2SDecryptionShare, *AdditiveShare) {
	return e2s.AllocateDecShare(), e2s.AllocateAddShare()
}

// AllocateDecShare allocates only a decryption share: needed as an intermediate buffer.
func (e2s *E2SProtocol) AllocateDecShare() *E2SDecryptionShare {
	return &E2SDecryptionShare{e2s.cks.AllocateShare()}
}

// AllocateAddShare allocates only an additive share.
func (e2s *E2SProtocol) AllocateAddShare() *AdditiveShare {
	return &AdditiveShare{e2s.cks.context.contextT.NewPoly()}
}

// GenSharesSlave is to be called by slaves to generate both the decryption share and the additive share.
func (e2s *E2SProtocol) GenSharesSlave(sk *bfv.SecretKey, ct *bfv.Ciphertext, decShareOut *E2SDecryptionShare, addShareOut *AdditiveShare) {
	//First step is to run the CKS protocol with s_out = 0
	e2s.cks.GenShare(sk.Get(), e2s.cks.context.contextQ.NewPoly(), ct, decShareOut.CKSShare)

	//We sample M_i, which will be returned as-is in addShareOut
	addShareOut.elem = e2s.cks.context.contextT.NewUniformPoly()

	//We encode M_i, so as to get delta*M_i in the InvNTT domain (where the ciphertext lies)
	e2s.encoder.EncodeUint(addShareOut.elem.GetCoefficients()[0], e2s.plain)

	//We subtract delta*M_i to the decryption share
	e2s.cks.context.contextQ.Sub(decShareOut.Poly, e2s.plain.Value()[0], decShareOut.Poly)

	return
}

//GenShareMaster is to be called by the master after aggregating all the slaves' decryption shares
//to get its own additive share
func (e2s *E2SProtocol) GenShareMaster(sk *bfv.SecretKey, ct *bfv.Ciphertext, decShareAgg *E2SDecryptionShare, addShareOut *AdditiveShare) {
	//First, we prepare the ciphertext to decrypt
	e2s.cks.context.contextQ.Copy(ct.Value()[0], e2s.poly)
	e2s.cks.context.contextQ.Add(e2s.poly, decShareAgg.Poly, e2s.poly) //ct[0] += sum(h_i)
	e2s.cks.context.contextQ.Copy(e2s.poly, e2s.cipher.Value()[0])
	e2s.cks.context.contextQ.Copy(ct.Value()[1], e2s.cipher.Value()[1])

	//We decrypt the ciphertext with our share of the ideal secret key
	decryptor := bfv.NewDecryptor(e2s.cks.context.params, sk)
	decryptor.Decrypt(e2s.cipher, e2s.plain)

	//As a last step, we decode the plaintext obtained, since we want the shares to be additive in Z_t^n
	addShareOut.elem.SetCoefficients([][]uint64{e2s.encoder.DecodeUint(e2s.plain)})

	return
}

//AggregateDecryptionShares pretty much describes itself. It is safe to have shareOut coincide with share1 or share2.
func (e2s *E2SProtocol) AggregateDecryptionShares(share1, share2, shareOut *E2SDecryptionShare) {
	e2s.cks.context.contextQ.Add(share1.Poly, share2.Poly, shareOut.Poly)
}

/******** Operations on additive shares********/

// SumAdditiveShares describes itself. It is safe to have shareOut coincide with either share1 or share2.
func (e2s *E2SProtocol) SumAdditiveShares(share1, share2, shareOut *AdditiveShare) {
	e2s.cks.context.contextT.Add(share1.elem, share2.elem, shareOut.elem)
}

// Equal compares coefficient-wise
func (x *AdditiveShare) Equal(m []uint64) bool {
	xcoeffs := x.elem.GetCoefficients()[0]

	if len(xcoeffs) != len(m) {
		return false
	}

	for i := range xcoeffs {
		if xcoeffs[i] != m[i] {
			return false
		}
	}

	return true
}

// GetCoeffs returns the coefficients (not copied)
func (x *AdditiveShare) GetCoeffs() []uint64 {
	return x.elem.Coeffs[0]
}

/******** Useful for tests ********/

// Various goroutines, each running the protocol as a node, need to provide their AdditiveShare to
// a common accumulator. The last one unlocks "done", awaking the master thread.
type ConcurrentAdditiveShareAccum struct {
	*sync.Mutex
	*AdditiveShare
	proto   *E2SProtocol // TODO: replace with context
	missing int
	done    *sync.Mutex
}

func NewConcurrentAdditiveShareAccum(params *bfv.Parameters, sigmaSmudging float64, nbParties int) *ConcurrentAdditiveShareAccum {
	proto := NewE2SProtocol(params, sigmaSmudging)
	c := &ConcurrentAdditiveShareAccum{
		Mutex:         &sync.Mutex{},
		AdditiveShare: proto.AllocateAddShare(),
		proto:         proto,
		missing:       nbParties,
		done:          &sync.Mutex{},
	}

	c.done.Lock()
	return c
}

func (accum *ConcurrentAdditiveShareAccum) Accumulate(share *AdditiveShare) {
	accum.Lock()
	defer accum.Unlock()

	accum.proto.SumAdditiveShares(accum.AdditiveShare, share, accum.AdditiveShare)
	accum.missing -= 1
	if accum.missing == 0 {
		accum.done.Unlock()
	}
}

func (accum *ConcurrentAdditiveShareAccum) WaitDone() {
	accum.done.Lock()
}
