package schema

type Option func(*cur) error

func At(p ...any) Option {
	return func(c *cur) error {
		bw := c.b.Writer()

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
	}
}
