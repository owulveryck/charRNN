package lstm

import (
	"context"
	"errors"
	"sync"

	"github.com/owulveryck/lstm/datasetter"
	G "gorgonia.org/gorgonia"
)

// the cost function
func (l *lstm) cost(dataSet datasetter.Trainer) (cost, perplexity *G.Node, err error) {
	hidden, cell, err := l.forwardStep(dataSet, l.prevHidden, l.prevCell, 0)
	if err != nil {
		return nil, nil, err
	}
	var loss, perp *G.Node
	// Evaluate the cost
	for i, computedVector := range dataSet.GetComputedVectors() {
		expectedValue, err := dataSet.GetExpectedValue(i)
		if err != nil {
			return nil, nil, err
		}
		logprob := G.Must(G.Neg(G.Must(G.Log(computedVector))))
		loss = G.Must(G.Slice(logprob, G.S(expectedValue)))
		log2prob := G.Must(G.Neg(G.Must(G.Log2(computedVector))))
		perp = G.Must(G.Slice(log2prob, G.S(expectedValue)))

		if cost == nil {
			cost = loss
		} else {
			cost = G.Must(G.Add(cost, loss))
		}
		G.WithName("Cost")(cost)

		if perplexity == nil {
			perplexity = perp
		} else {
			perplexity = G.Must(G.Add(perplexity, perp))
		}
	}
	l.prevHidden = hidden
	l.prevCell = cell
	return
}

// TrainingInfos returns info about the current training process
type TrainingInfos struct {
	Step       int
	Perplexity float32
	Cost       float32
}

// Train the model
func (m *Model) Train(ctx context.Context, dset datasetter.FullTrainer, solver G.Solver, pauseChan <-chan struct{}) (<-chan TrainingInfos, <-chan error) {
	infoChan := make(chan TrainingInfos, 0)
	step := 0
	errc := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	paused := false

	go func() {
		if len(pauseChan) != 0 {
			errc <- errors.New("pauseChan must not be buffered")
			wg.Done()
			return
		}
		for {
			select {
			case <-ctx.Done():
				errc <- nil
				wg.Done()
				return
			case <-pauseChan:
				paused = true
			default:
				if paused {
					<-pauseChan
					paused = false
				}
				step++
				lstm := m.newLSTM()
				trainer, err := dset.GetTrainer()
				if err != nil {
					errc <- err
					wg.Done()
					return
				}
				cost, perplexity, err := lstm.cost(trainer)
				if err != nil {
					errc <- err
					wg.Done()
					return
				}
				//g := m.g.SubgraphRoots(cost, perplexity)
				machine := G.NewLispMachine(lstm.g)
				if err := machine.RunAll(); err != nil {
					errc <- err
					wg.Done()
					return
				}
				// send infos about this execution step in a non blocking channel
				select {
				case infoChan <- TrainingInfos{
					Perplexity: perplexity.Value().Data().(float32),
					Cost:       cost.Value().Data().(float32),
					Step:       step,
				}:
				}
				solver.Step(G.Nodes{
					lstm.biasC, lstm.biasF, lstm.biasI, lstm.biasO, lstm.biasY,
					lstm.uc, lstm.uf, lstm.ui, lstm.uo,
					lstm.wc, lstm.wf, lstm.wi, lstm.wo, lstm.wy})
			}
		}
	}()
	go func() {
		wg.Wait()
		close(infoChan)
	}()
	return infoChan, errc
}
