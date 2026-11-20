package xlsb

// The types of the records this library reads ([MS-XLSB] 2.3), by the
// names the specification gives them.
const (
	// Cells, in a sheet's data. A cell record gives its column; a "short"
	// one (BrtShort*) is in the column after the cell before it.
	RowHdr       = 0
	CellBlank    = 1
	CellRk       = 2
	CellError    = 3
	CellBool     = 4
	CellReal     = 5
	CellSt       = 6
	CellIsst     = 7
	FmlaString   = 8
	FmlaNum      = 9
	FmlaBool     = 10
	FmlaError    = 11
	ShortBlank   = 12
	ShortRk      = 13
	ShortError   = 14
	ShortBool    = 15
	ShortReal    = 16
	ShortSt      = 17
	ShortIsst    = 18
	ShortRString = 61
	CellRString  = 62
	ArrFmla      = 426 // an array formula, after its first cell
	ShrFmla      = 427 // a shared formula, after its first cell

	// Shared strings.
	SSTItem  = 19
	BeginSst = 159

	// The workbook.
	Name       = 39
	BeginBook  = 131
	WbProp     = 153
	BundleSh   = 156
	BeginSheet = 129

	// The books a formula's references may be to, and the sheets of
	// them that 3-D references index.
	SupBookSrc  = 355
	SupSelf     = 357
	SupSame     = 358
	ExternSheet = 362
	SupAddin    = 667

	// Styles.
	Font            = 43
	Fmt             = 44
	XF              = 47
	BeginFonts      = 611
	EndFonts        = 612
	BeginCellXFs    = 617
	EndCellXFs      = 618
	BeginStyleSheet = 278

	// A sheet's own records.
	ColInfo           = 60
	DVal              = 64
	BeginSheetData    = 145
	EndSheetData      = 146
	WsProp            = 147
	MergeCell         = 176
	BeginColInfos     = 390
	EndColInfos       = 391
	HLink             = 494
	BeginDVals        = 573
	EndDVals          = 574
	Drawing           = 550
	LegacyDrawing     = 551
	ListPart          = 661
	BeginMergeCells   = 177
	EndMergeCells     = 178
	BeginListParts    = 660
	EndListParts      = 662
	BeginOleObjects   = 638
	OleObject         = 639
	BeginActiveXCtrls = 643

	// Comments.
	BeginComments     = 628
	CommentAuthor     = 632
	BeginComment      = 635
	EndComment        = 636
	CommentText       = 637
	BeginCommentList  = 633
	BeginCommentAuths = 630

	// A table's part.
	BeginList     = 343
	TableStyleClt = 513
)
