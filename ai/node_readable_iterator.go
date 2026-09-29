package ai

// asyncIterator is Readable.prototype[Symbol.asyncIterator] with default options: the native async generator createAsyncIterator (lib/internal/streams/readable.js) on the jsAsyncGenerator model. The generator body resumes synchronously inside next() and inside await continuations, as V8 runs it.
//
// The body mirrors the source statement by statement. `yield chunk` awaits its operand and completes the head request; a return completion delivered to the yield unwinds through the finally block after awaiting its operand; a bare `return;` does not await.
func (readable *nodeReadable) asyncIterator() *jsAsyncGenerator[[]byte] {
	return newJSAsyncGenerator(readable.executor, func(body *jsGeneratorBody[[]byte]) ([]byte, error) {
		callback := func() {}
		// The `error` variable: undefined while streaming, null after a clean end, an Error after a failure.
		var finished nodeIteratorFinish
		var failure error

		readableListener := readable.on("readable", func(error) {
			// next(): the `this === stream` branch runs the pending callback.
			pending := callback
			callback = func() {}
			pending()
		})
		cleanup := nodeEndOfStream(readable, func(err error) {
			if err != nil {
				finished, failure = nodeIteratorFinishedError, err
			} else {
				finished, failure = nodeIteratorFinishedOK, nil
			}
			pending := callback
			callback = func() {}
			pending()
		})

		// The finally block. (error || options?.destroyOnReturn !== false) is always true without options, so the second operand decides.
		finally := func() {
			if finished == nodeIteratorStreaming || readable.autoDestroy {
				readable.destroyer(nil)
				return
			}
			readable.off("readable", readableListener)
			cleanup()
		}

		for {
			var chunk []byte
			if !readable.destroyed {
				chunk = readable.read(nodeReadAll)
			}
			switch {
			case chunk != nil:
				if err := body.yield(jsValue(chunk)); err != nil {
					finally()
					return nil, err
				}
			case finished == nodeIteratorFinishedError:
				// catch (err) { error = aggregateTwoErrors(error, err); throw error }
				finally()
				return nil, failure
			case finished == nodeIteratorFinishedOK:
				finally()
				return nil, nil
			default:
				// await new Promise(next): next(resolve) stores resolve as the callback.
				wake := newJSPromise[struct{}](readable.executor)
				callback = func() { wake.resolve(jsValue(struct{}{})) }
				_, _ = jsAwait(body, jsPromiseOperand(wake))
			}
		}
	})
}

type nodeIteratorFinish uint8

const (
	nodeIteratorStreaming nodeIteratorFinish = iota
	nodeIteratorFinishedOK
	nodeIteratorFinishedError
)
