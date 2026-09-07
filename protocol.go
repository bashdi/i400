package i400

import (
	"encoding/binary"
	"fmt"
)

const (
	PortMapperPort = 449

	DatabasePort              = 8471
	SecureDatabasePort        = 9471
	DatabaseServiceName       = "as-database"
	DatabaseServiceSecureName = "as-database-s"

	SignonPort              = 8476
	SecureSignonPort        = 9476
	SignonServiceName       = "as-signon"
	SignonServiceSecureName = "as-signon-s"

	DatabaseServerID       = 0xE004
	NativeDatabaseServerID = 0xE005
	SignonServerID         = 0xE009
	HostConnServerID       = 0xE00B

	RequestExchangeRandomSeeds = 0x7001
	RequestStartServer         = 0x7002
	RequestExchangeAttributes  = 0x7003
	RequestSignonInfo          = 0x7004

	ReplyExchangeRandomSeeds = 0xF001
	ReplyStartServer         = 0xF002
	ReplyExchangeAttributes  = 0xF003
	ReplySignonInfo          = 0xF004

	FunctionPrepare             = 0x1800
	FunctionDescribe            = 0x1801
	FunctionDescribeParmMarker  = 0x1802
	FunctionPrepareDescribe     = 0x1803
	FunctionOpenDescribe        = 0x1804
	FunctionExecute             = 0x1805
	FunctionExecuteImmediate    = 0x1806
	FunctionCommit              = 0x1807
	FunctionRollback            = 0x1808
	FunctionConnect             = 0x1809
	FunctionClose               = 0x180A
	FunctionFetch               = 0x180B
	FunctionStreamFetch         = 0x180C
	FunctionAddLibraryList      = 0x180C
	FunctionPrepareExecute      = 0x180D
	FunctionOpenDescribeFetch   = 0x180E
	FunctionCreatePackage       = 0x180F
	FunctionClearPackage        = 0x1810
	FunctionDeletePackage       = 0x1811
	FunctionExecuteOpenDescribe = 0x1812
	FunctionEndStreamFetch      = 0x1813
	FunctionCreateRPB           = 0x1D00
	FunctionChangeRPB           = 0x1D03
	FunctionDeleteRPB           = 0x1D02
	FunctionChangeDescriptor    = 0x1E00
	FunctionDeleteDescriptor    = 0x1E01
	FunctionReturnPackage       = 0x1815
	FunctionRetrieveLobData     = 0x1816
	FunctionWriteLobData        = 0x1817
	FunctionCancel              = 0x1818
	FunctionFreeLob             = 0x1819
	FunctionTestConnection      = 0x0000
	FunctionEndJob              = 0x1FFF

	cursorReuseNo        byte = 0xF0
	cursorReuseYes       byte = 0xF1
	cursorReuseResultSet byte = 0xF2

	FunctionSetAttributes      = 0x1F80
	FunctionRetrieveAttributes = 0x1F81

	HeaderSize   = 20
	TemplateSize = 20

	CodePointClientVersion        = 0x1101
	CodePointClientDSLevel        = 0x1102
	CodePointClientSeed           = 0x1103
	CodePointUserID               = 0x1104
	CodePointPassword             = 0x1105
	CodePointCurrentSignonDate    = 0x1106
	CodePointLastSignonDate       = 0x1107
	CodePointPasswordExpireDate   = 0x1108
	CodePointServerCCSID          = 0x1114
	CodePointPasswordLevel        = 0x1119
	CodePointJobName              = 0x111F
	CodePointReturnErrorMessages  = 0x1128
	CodePointPasswordWarning      = 0x112C
	CodePointAdditionalAuthFactor = 0x112F
	CodePointVerificationID       = 0x1130
	CodePointClientIPAddress      = 0x1131

	CodePointClientCCSID             = 0x1113
	CodePointNamingConvention        = 0x380C
	CodePointDefaultSQLLibrary       = 0x380F
	CodePointJobIdentifier           = 0x3826
	CodePointCommitmentControlLevel  = 0x380E
	CodePointDateFormat              = 0x3807
	CodePointDateSeparator           = 0x3808
	CodePointTimeFormat              = 0x3809
	CodePointTimeSeparator           = 0x380A
	CodePointDecimalSeparator        = 0x380B
	CodePointClientFunctionalLevel   = 0x3803
	CodePointLanguageFeatureCode     = 0x3802
	CodePointTranslateIndicator      = 0x00F0
	CodePointUseExtendedFormats      = 0x00F1
	CodePointUseSuperExtendedFormats = 0x00F2
	CodePointTrueAutoCommitIndicator = 0x3824

	CodePointMessageID                    = 0x3801
	CodePointFirstLevelMessageText        = 0x3802
	CodePointSecondLevelMessageText       = 0x3803
	CodePointServerAttributes             = 0x3804
	CodePointDataFormat                   = 0x3805
	CodePointResultData                   = 0x3806
	CodePointSQLCA                        = 0x3807
	CodePointParameterMarkerFormat        = 0x3808
	CodePointPackageInfo                  = 0x380B
	CodePointExtendedDataFormat           = 0x380C
	CodePointExtendedParameterMarker      = 0x380D
	CodePointExtendedResultData           = 0x380E
	CodePointLOBLocatorData               = 0x380F
	CodePointCurrentLOBLength             = 0x3810
	CodePointExtendedColumnDescriptors    = 0x3811
	CodePointSuperExtendedDataFormat      = 0x3812
	CodePointSuperExtendedParameterMarker = 0x3813
	CodePointCursorAttributes             = 0x3814
	CodePointAlternateServer              = 0x3846
	CodePointVariableResultData           = 0x3901
	CodePointXIDData                      = 0x38A1

	CodePointStatementText            = 0x3807
	CodePointPrepareStatementName     = 0x3806
	CodePointPackageName              = 0x3804
	CodePointPrepareOption            = 0x3808
	CodePointOpenAttributes           = 0x3809
	CodePointDescribeOption           = 0x380A
	CodePointCursorName               = 0x380B
	CodePointBlockingFactor           = 0x380C
	CodePointScrollableCursorFlag     = 0x380D
	CodePointFetchScrollOption        = 0x380E
	CodePointHoldIndicator            = 0x380F
	CodePointReuseIndicator           = 0x3810
	CodePointParameterMarkerData      = 0x3811
	CodePointStatementType            = 0x3812
	CodePointParameterMarkerBlockInd  = 0x3814
	CodePointReturnSize               = 0x3815
	CodePointLOBLocatorHandle         = 0x3818
	CodePointRequestedSize            = 0x3819
	CodePointStartOffset              = 0x381A
	CodePointCompressionIndicator     = 0x381B
	CodePointParameterMarkerDataExt   = 0x381F
	CodePointColumnIndex              = 0x3828
	CodePointExtendedColumnDescOption = 0x3829
	CodePointResultSetHoldability     = 0x3830
	CodePointExtendedStatementText    = 0x3831
	CodePointVariableFieldCompression = 0x3833
	CodePointListOfLibraries          = 0x3813
)

type Header struct {
	Length         uint32
	HeaderID       uint16
	ServerID       uint16
	CSInstance     uint32
	CorrelationID  uint32
	TemplateLength uint16
	ReqRepID       uint16
}

func (h Header) MarshalBinary() ([]byte, error) {
	buf := make([]byte, HeaderSize)
	binary.BigEndian.PutUint32(buf[0:4], h.Length)
	binary.BigEndian.PutUint16(buf[4:6], h.HeaderID)
	binary.BigEndian.PutUint16(buf[6:8], h.ServerID)
	binary.BigEndian.PutUint32(buf[8:12], h.CSInstance)
	binary.BigEndian.PutUint32(buf[12:16], h.CorrelationID)
	binary.BigEndian.PutUint16(buf[16:18], h.TemplateLength)
	binary.BigEndian.PutUint16(buf[18:20], h.ReqRepID)
	return buf, nil
}

func ParseHeader(data []byte) (Header, error) {
	if len(data) < HeaderSize {
		return Header{}, fmt.Errorf("header too short: %d", len(data))
	}
	if binary.BigEndian.Uint32(data[0:4]) == uint32(len(data)) {
		return Header{
			Length:         binary.BigEndian.Uint32(data[0:4]),
			HeaderID:       binary.BigEndian.Uint16(data[4:6]),
			ServerID:       binary.BigEndian.Uint16(data[6:8]),
			CSInstance:     binary.BigEndian.Uint32(data[8:12]),
			CorrelationID:  binary.BigEndian.Uint32(data[12:16]),
			TemplateLength: binary.BigEndian.Uint16(data[16:18]),
			ReqRepID:       binary.BigEndian.Uint16(data[18:20]),
		}, nil
	}
	return Header{
		Length:         uint32(len(data) + 4),
		HeaderID:       binary.BigEndian.Uint16(data[0:2]),
		ServerID:       binary.BigEndian.Uint16(data[2:4]),
		CSInstance:     binary.BigEndian.Uint32(data[4:8]),
		CorrelationID:  binary.BigEndian.Uint32(data[8:12]),
		TemplateLength: binary.BigEndian.Uint16(data[12:14]),
		ReqRepID:       binary.BigEndian.Uint16(data[14:16]),
	}, nil
}

func AppendLLCP(dst []byte, codePoint uint16, payload []byte) []byte {
	length := uint32(len(payload) + 6)
	dst = binary.BigEndian.AppendUint32(dst, length)
	dst = binary.BigEndian.AppendUint16(dst, codePoint)
	return append(dst, payload...)
}

func ParseLLCP(data []byte) (codePoint uint16, payload []byte, rest []byte, err error) {
	if len(data) < 6 {
		return 0, nil, nil, fmt.Errorf("llcp too short: %d", len(data))
	}
	length := binary.BigEndian.Uint32(data[0:4])
	if length < 6 {
		return 0, nil, nil, fmt.Errorf("invalid llcp length: %d", length)
	}
	if int(length) > len(data) {
		return 0, nil, nil, fmt.Errorf("llcp length %d exceeds buffer %d", length, len(data))
	}
	codePoint = binary.BigEndian.Uint16(data[4:6])
	payload = data[6:int(length)]
	rest = data[int(length):]
	return codePoint, payload, rest, nil
}
