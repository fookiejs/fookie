package ast

import "time"

type Schema struct {
	Models    []*Model
	Externals []*External
	Modules   []*Module
}

type Model struct {
	Name   string
	Fields []*Field
	CRUD   map[string]*Operation
	Uses   []string
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
	Type    string
	Role    *Block
	Rule    *Block
	Modify  *Block
	Effect     *Block
	Compensate *Block
	Where      *WhereClause
	OrderBy []*OrderBy
	Cursor  *Cursor
	Select  []*SelectField
	Lock    bool
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
	Where  []Expression
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

type WhereClause struct {
	Conditions []Expression
}

type OrderBy struct {
	Field string
	Desc  bool
}

type Cursor struct {
	Size   int
	After  *string
}

type External struct {
	Name   string
	Input  map[string]string
	Output map[string]string
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
