package ast

import "time"

type Schema struct {
	Models    []*Model
	Externals []*External
	Modules   []*Module
	Seeds     []*SeedBlock
	Crons     []*CronBlock
	Setups    []*SeedBlock
}

type SeedBlock struct {
	Entries []*SeedEntry
}

type SeedEntry struct {
	Model    string
	KeyField string
	Records  []map[string]interface{}
}

type CronBlock struct {
	Entries []*CronEntry
}

type CronEntry struct {
	Name     string
	CronExpr string
	Body     *Block
}

type IndexDef struct {
	Unique  bool     // true = UNIQUE INDEX
	Columns []string // column names in order (e.g. ["email"], ["tenant_id","email"])
	Desc    []bool   // parallel to Columns: true = DESC
	Where   string   // optional partial-index WHERE clause (raw SQL)
}

type Model struct {
	Name    string
	Fields  []*Field
	CRUD    map[string]*Operation
	Uses    []string
	Indexes []IndexDef
}

type Field struct {
	Name        string
	Type        FieldType
	Relation    *string
	Constraints []string
}

type FieldType string

const (
	TypeString    FieldType = "string"
	TypeNumber    FieldType = "number"
	TypeBoolean   FieldType = "boolean"
	TypeID        FieldType = "id"
	TypeDate      FieldType = "date"
	TypeTimestamp FieldType = "timestamp"
	TypeJSON      FieldType = "json"
	TypeRelation  FieldType = "relation"

	TypeEmail      FieldType = "email"
	TypeURL        FieldType = "url"
	TypePhone      FieldType = "phone"
	TypeUUID       FieldType = "uuid"
	TypeCoordinate FieldType = "coordinate"
	TypeColor      FieldType = "color"
	TypeCurrency   FieldType = "currency"
	TypeLocale     FieldType = "locale"
	TypeIBAN       FieldType = "iban"
	TypeIPAddress  FieldType = "ipaddress"
)

type Operation struct {
	Type       string
	Field      string
	Role       *Block
	Rule       *Block
	Modify     *Block
	Effect     *Block
	Compensate *Block
	Filter     *FilterClause
	OrderBy    []*OrderBy
	Cursor     *Cursor
	Select     []*SelectField
}

type SelectField struct {
	Alias string
	Expr  SelectExpr
}

type SelectExpr interface {
	selectExprMarker()
}

type PlainField struct {
	Path []string
}

func (PlainField) selectExprMarker() {}

type AggregateFunc struct {
	Fn    string
	Field []string
}

func (AggregateFunc) selectExprMarker() {}

type Block struct {
	Statements []Statement
}

type Statement interface {
	statementMarker()
}

type Assignment struct {
	Name   string
	Value  Expression
	LineNo int
}

func (Assignment) statementMarker() {}

type ModifyAssignment struct {
	Field  string
	Value  Expression
	LineNo int
}

func (ModifyAssignment) statementMarker() {}

type PredicateExpr struct {
	Expr   Expression
	LineNo int
}

func (PredicateExpr) statementMarker()  {}
func (PredicateExpr) expressionMarker() {}

type Expression interface {
	expressionMarker()
}

type ExternalCall struct {
	Name   string
	Params map[string]Expression
	LineNo int
}

func (ExternalCall) expressionMarker() {}

type BuiltinCall struct {
	Name   string
	Args   []Expression
	LineNo int
}

func (BuiltinCall) expressionMarker() {}

type ReadQuery struct {
	Model  string
	Filter []Expression
	Lock   bool
	LineNo int
}

func (ReadQuery) expressionMarker() {}

type FieldAccess struct {
	Object string
	Fields []string
	LineNo int
}

func (FieldAccess) expressionMarker() {}

type Literal struct {
	Value  interface{}
	LineNo int
}

func (Literal) expressionMarker() {}

type BinaryOp struct {
	Left   Expression
	Op     string
	Right  Expression
	LineNo int
}

func (BinaryOp) expressionMarker() {}

type UnaryOp struct {
	Op     string
	Right  Expression
	LineNo int
}

func (UnaryOp) expressionMarker() {}

type InExpr struct {
	Left   Expression
	Values []Expression
	LineNo int
}

func (InExpr) expressionMarker() {}

type ArrayLiteral struct {
	Items  []Expression
	LineNo int
}

func (ArrayLiteral) expressionMarker() {}

type EffectUpdateStmt struct {
	Model  string
	IDExpr Expression
	Fields []*ModifyAssignment
	LineNo int
}

func (*EffectUpdateStmt) statementMarker() {}

type EffectDeleteStmt struct {
	Model  string
	IDExpr Expression
	LineNo int
}

func (*EffectDeleteStmt) statementMarker() {}

type EffectNotifyStmt struct {
	RoomName string
	Payload  map[string]Expression
	LineNo   int
}

func (*EffectNotifyStmt) statementMarker() {}

type ForIn struct {
	Var      string
	Iterable Expression
	Body     *Block
	LineNo   int
}

func (ForIn) statementMarker() {}

type FilterClause struct {
	Conditions []Expression
}

type OrderBy struct {
	Field string
	Desc  bool
}

type Cursor struct {
	Size  int
	After *string
}

type External struct {
	Name   string
	Body   map[string]string
	Output map[string]string

	// Retry policy — configurable from FQL.
	// RetryMax: max attempts before marking failed (0 → default 3).
	// RetryBackoff: "none" | "linear" | "exponential" (default "exponential").
	// RetryMaxDelay: cap for backoff in seconds (0 → no cap).
	RetryMax      int
	RetryBackoff  string
	RetryMaxDelay int
}

type Module struct {
	Name   string
	Role   *Block
	Rule   *Block
	Modify *Block
	Effect *Block
}

type Context struct {
	Principal     map[string]interface{}
	Input         map[string]interface{}
	Output        map[string]interface{}
	Variables     map[string]interface{}
	Timestamp     time.Time
	TransactionID string
}

type OperationResult struct {
	RowID      string
	Status     string
	Output     map[string]interface{}
	Errors     []string
	ExecutedAt time.Time
	Duration   time.Duration
}
