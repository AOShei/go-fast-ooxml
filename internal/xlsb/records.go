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
	SupBookSrc      = 355
	SupSelf         = 357
	SupSame         = 358
	PlaceholderName = 361 // a name used through the link before it: an add-in's function, a sheet's name
	ExternSheet     = 362
	SupAddin        = 667

	// A part of a link to another workbook (External Link): the kind of
	// link, the other workbook's sheets, and its names a formula uses.
	SupTabs      = 359
	BeginSupBook = 360
	SupNameStart = 577
	SupNameBits  = 586

	// Pivot tables, and the caches they are drawn from.
	PivotCacheID    = 386 // in the workbook: a cache's part (BrtBeginPivotCacheID)
	BeginSXView     = 280 // a pivot table: its name
	BeginSXLocation = 314 // where it is on the sheet

	// A pivot cache's definition: its source, and its fields with the
	// values records refer to.
	BeginPivotCacheDef = 179
	BeginPCDField      = 183
	EndPCDField        = 184
	BeginPCDSource     = 185
	BeginPCDSRange     = 187
	BeginPCDFAtbl      = 189
	EndPCDFAtbl        = 190
	BeginPCDIRun       = 191
	BeginPCDSCSet      = 217
	BeginPCDFGroup     = 219
	EndPCDFGroup       = 220
	BeginPName         = 255

	// A value of a pivot cache (BrtPCDI*), and the same with more said of
	// it (BrtPCDIA*); a record of a cache, its values packed, or given by
	// the value records that follow.
	PCDIMissing   = 20
	PCDINumber    = 21
	PCDIBoolean   = 22
	PCDIError     = 23
	PCDIString    = 24
	PCDIDatetime  = 25
	PCDIIndex     = 26
	PCDIAMissing  = 27
	PCDIANumber   = 28
	PCDIABoolean  = 29
	PCDIAError    = 30
	PCDIAString   = 31
	PCDIADatetime = 32
	PCRRecord     = 33
	PCRRecordDt   = 34

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
	EndList       = 344
	BeginListCol  = 347
	TableStyleClt = 513
)
