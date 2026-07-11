package schema

type (
	Option interface {
		apply(a *Applier) error
	}

	opt func(a *Applier) error

	use struct {
		a *Applier
	}
)

func Use(a *Applier) Option {
	if a == nil {
		panic("you need an Applier")
	}

	return use{a: a}
}

func At(p ...any) Option {
	return opt(func(c *Applier) error {
		bw := c.b.Writer()
		c.at = c.at[:0]

		for _, key := range p {
			var op Opcode

			if s, ok := key.([]byte); ok {
				op = bw.Span(Key, s)
			} else if s, ok := key.(string); ok {
				op = bw.Span(Key, []byte(s))
			} else if x, ok := key.(int); ok {
				op = makeImm(IntLit, x)
			} else if key == Each {
				op = Each
			} else {
				return &Error{
					Message: "Invalid At key",
					Op:      None,
				}
			}

			c.at = append(c.at, op)
		}

		return nil
	})
}

func (o use) apply(a *Applier) error { return nil }
func (o opt) apply(a *Applier) error { return o(a) }
