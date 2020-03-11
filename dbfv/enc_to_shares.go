//Encryption-to-shares is the protocol described in Section IV.E.1 of the paper.
//It separates parties P_2,...,P_n (called slaves in this file) from party P_1 (called master).
//The slaves behave similarly to the CKS protocol: the difference is that they sample M_i from Z_t^n
//(which becomes their additive share of the plaintext), and subtract delta*M_i from their decryption share.
//The master behaves differently: it collects all the decryption shares of the slaves, sums them, and performs a
//BFV-decryption with its own secret key share (as described in the paper) to get its additive share
//(it does not produce a decryption share).

package dbfv

import (
	"errors"
	"github.com/ldsec/lattigo/bfv"
	"github.com/ldsec/lattigo/ring"
)

//E2SProtocol contains all the parameters needed to perform the various steps of the protocol.
type E2SProtocol struct {
	cks     *CKSProtocol //CKSProtocol is not embedded to have control over the exposed methods
	encoder bfv.Encoder  //TODO: is this right?

	//Just memory pools
	plain  *bfv.Plaintext
	cipher *bfv.Ciphertext
	poly   *ring.Poly
}

//E2SDecryptionShare represents the decryption share produced by slaves, which needs to be disclosed and
//collected by the master. It is an element of R_q.
type E2SDecryptionShare struct {
	CKSShare
}

//AdditiveShare represents the additive share of the plaintext the party possesses after running the protocol.
//The additive shares are elements of Z_t^n, and add up to the original clear vector, not to its plaintext-encoding.
type AdditiveShare struct {
	coeffs []uint64
}

//NewE2SProtocol allocates a protocol struct
func NewE2SProtocol(params *bfv.Parameters, sigmaSmudging float64) *E2SProtocol {
	cks := NewCKSProtocol(params, sigmaSmudging)

	return &E2SProtocol{cks,
		bfv.NewEncoder(params),
		bfv.NewPlaintext(params),
		bfv.NewCiphertext(params, 1),
		cks.context.contextQ.NewPoly()}
}

//AllocateShares allocates both shares: they are both needed by both leaves and slaves
func (e2s *E2SProtocol) AllocateShares() (E2SDecryptionShare, AdditiveShare) {
	return E2SDecryptionShare{e2s.cks.AllocateShare()},
		AdditiveShare{make([]uint64, e2s.cks.context.n)}
}

//GenSharesLeaf is to be called by leaves to generate both the decryption share and the additive share
func (e2s *E2SProtocol) GenSharesLeaf(sk bfv.SecretKey, ct *bfv.Ciphertext, decShareOut E2SDecryptionShare, addShareOut AdditiveShare) {
	//First step is to run the CKS protocol with s_out = 0
	e2s.cks.GenShare(sk.Get(), e2s.cks.context.contextQ.NewPoly(), ct, decShareOut.CKSShare)

	//We sample M_i, which will be returned as-is in addShareOut
	addShareOut.coeffs = e2s.cks.context.contextT.NewUniformPoly().GetCoefficients()[0]

	//We encode M_i, so as to get delta*M_i in the InvNTT domain (where the ciphertext lies)
	e2s.encoder.EncodeUint(addShareOut.coeffs, e2s.plain) //TODO: is this right?

	//We subtract delta*M_i to the decryption share
	e2s.cks.context.contextQ.Sub(decShareOut.Poly, e2s.plain.Value()[0], decShareOut.Poly)

	return
}

//GenShareMaster is to be called by the master after aggregating all the slaves' decryption shares
//to get its own additive share
func (e2s *E2SProtocol) GenShareMaster(sk *bfv.SecretKey, ct *bfv.Ciphertext, decShareAgg E2SDecryptionShare, addShareOut AdditiveShare) {
	//First, we prepare the ciphertext to decrypt
	e2s.cks.context.contextQ.Copy(ct.Value()[0], e2s.poly)
	e2s.cks.context.contextQ.Add(e2s.poly, decShareAgg.Poly, e2s.poly) //ct[0] += sum(h_i)
	e2s.cipher.SetValue([]*ring.Poly{e2s.poly, ct.Value()[1]})

	//We decrypt the ciphertext with our share of the ideal secret key
	decryptor := bfv.NewDecryptor(e2s.cks.context.params, sk) //TODO: shall I make it part of the E2SProtocol struct?
	decryptor.Decrypt(e2s.cipher, e2s.plain)

	//As a last step, we decode the plaintext obtained, since we want the shares to be additive in Z_t^n
	addShareOut.coeffs = e2s.encoder.DecodeUint(e2s.plain)

	return
}

//AggregateDecryptionShares pretty much describes itself. It is safe to have shareOut coincide with share1 or share2.
func (e2s *E2SProtocol) AggregateDecryptionShares(share1, share2, shareOut E2SDecryptionShare) {
	e2s.cks.context.contextQ.Add(share1.Poly, share2.Poly, shareOut.Poly)
}

/******** Operations on additive shares********/

//NewAdditiveShare pretty much describes itself.
func NewAdditiveShare(coeffs []uint64) AdditiveShare {
	return AdditiveShare{coeffs: coeffs}
}

//GetCoeffs returns a copy of the coefficients.
func (x AdditiveShare) GetCoeffs() []uint64 {
	newCoeffs := make([]uint64, len(x.coeffs))
	copy(newCoeffs, x.coeffs)
	return newCoeffs
}

//GetNbCoeffs returns the number of coefficients.
func (x AdditiveShare) GetNbCoeffs() uint64 {
	return uint64(len(x.coeffs))
}

//SetCoeffs copies the coefficients (requires x.coeffs and coeffs to have the same length)
func (x AdditiveShare) SetCoeffs(coeffs []uint64) error {
	if len(x.coeffs) != len(coeffs) {
		return errors.New("Cannot add two additive shares of different length")
	}

	for i := range x.coeffs {
		x.coeffs[i] = coeffs[i]
	}

	return nil
}

//Add requires x.coeffs and y.coeffs to have the same length
func (x AdditiveShare) Add(y AdditiveShare) error {
	if len(x.coeffs) != len(y.coeffs) {
		return errors.New("Cannot add two additive shares of different length")
	}

	for i := range x.coeffs {
		x.coeffs[i] += y.coeffs[i]
	}

	return nil
}

//Equals compares the shares coefficient-wise
func (x AdditiveShare) Equals(y AdditiveShare) bool {
	if len(x.coeffs) != len(y.coeffs) {
		return false
	}

	for i := range x.coeffs {
		if x.coeffs[i] != y.coeffs[i] {
			return false
		}
	}

	return true
}
