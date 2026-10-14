package com.ibm.as400.access;

import java.io.FileOutputStream;
import java.io.OutputStreamWriter;
import java.io.PrintWriter;
import java.io.Writer;
import java.util.Random;
import java.util.zip.GZIPOutputStream;

/**
 * Golden vectors for the Go driver's bidi layer (root package, bidi_integration_test.go).
 *
 * Usage (classes compiled from the JTOpen sources, see the Go test header):
 *   java -cp classes com.ibm.as400.access.BidiDriverGolden conv    <out.txt.gz>
 *   java -cp classes com.ibm.as400.access.BidiDriverGolden strings <out.txt.gz>
 *
 * "conv" calls the real ConvTable of each bidi CCSID (ConvTableBidiMap) with the
 * BidiConversionProperties that JTOpen's JDBC driver builds in SQLChar (convertFromRawBytes /
 * convertToRawBytes): the bidi string type is the connection setting; when the setting is -1
 * (the DSN "not set" value) the CCSID's string type (ConvTable.bidiStringType_) is used. Then
 * new BidiConversionProperties(type), setBidiImplicitReordering(..), setBidiNumericOrderingRoundTrip(..).
 * Lines:  C  ccsid  dsnType  implicit  numeric  op  in  out
 *   op D: in = bytes (hex), out = UTF-16 units returned by byteArrayToString (hex).
 *   op E: in = UTF-16 units (hex), out = bytes returned by stringToByteArray (hex).
 *   "-" is an empty value.
 *
 * "strings" calls the public static string functions of AS400BidiTransform that the JDBC driver
 * applies to SQL text and column names. Lines (in, out are string types):
 *   T in out inHex outHex    bidiTransform
 *   S in out inHex outHex    SQL_statement_reordering
 *   M in out inHex outHex    meta_data_reordering
 */
public class BidiDriverGolden {

    static final int[] CCSIDS = { 420, 424, 425, 856, 862, 864, 867, 916, 1046, 1089, 1255, 1256,
            5012, 5351, 5352, 8612, 8616, 9238, 12708, 12712, 13488, 16804, 17248, 62211, 62224, 62235, 62245, 62251 };
    static final int[] DSN_TYPES = { -1, 0, 4, 5, 6, 7, 8, 9, 10, 11 };

    public static void main(String[] args) throws Exception {
        String mode = args[0];
        Writer w = new OutputStreamWriter(new GZIPOutputStream(new FileOutputStream(args[1])), "UTF-8");
        PrintWriter out = new PrintWriter(w);
        if (mode.equals("conv")) {
            conv(out);
        } else if (mode.equals("strings")) {
            strings(out);
        } else {
            throw new IllegalArgumentException(mode);
        }
        out.close();
    }

    static String hexBytes(byte[] b, int n) {
        if (n == 0) return "-";
        StringBuilder sb = new StringBuilder();
        for (int i = 0; i < n; i++) sb.append(String.format("%02X", b[i] & 0xFF));
        return sb.toString();
    }

    static String hexString(String s) {
        if (s.length() == 0) return "-";
        StringBuilder sb = new StringBuilder();
        for (int i = 0; i < s.length(); i++) sb.append(String.format("%04X", (int) s.charAt(i)));
        return sb.toString();
    }

    static void conv(PrintWriter out) throws Exception {
        Random rnd = new Random(20260614L);
        int configs = 0, lines = 0;
        for (int ccsid : CCSIDS) {
            ConvTable table;
            try {
                table = ConvTable.getTable(ccsid, null);
            } catch (Exception e) {
                continue;
            }
            if (!(table instanceof ConvTableBidiMap)) continue;
            // Characters the table produces: every byte decoded through the table alone.
            BidiConversionProperties none = new BidiConversionProperties(BidiStringType.NONE);
            StringBuilder charSet = new StringBuilder();
            for (int b = 0; b < 256; b++) {
                String s = table.byteArrayToString(new byte[] { (byte) b }, 0, 1, none);
                charSet.append(s.charAt(0));
            }
            String chars = charSet.toString();
            // Configurations (dsn value, implicit, numeric), as the DSN options produce them.
            int[][] cfgs = new int[DSN_TYPES.length + 3][];
            int k = 0;
            for (int t : DSN_TYPES) cfgs[k++] = new int[] { t, 1, 0 };
            cfgs[k++] = new int[] { 5, 0, 0 };
            cfgs[k++] = new int[] { 5, 1, 1 };
            cfgs[k++] = new int[] { -1, 0, 1 };
            for (int[] cfg : cfgs) {
                int dsn = cfg[0];
                boolean implicit = cfg[1] == 1;
                boolean numeric = cfg[2] == 1;
                int type = dsn;
                if (type == -1) type = table.bidiStringType_; // SQLChar: "not set" uses the CCSID type
                for (int n = 0; n < 40; n++) {
                    int len = rnd.nextInt(31);
                    byte[] raw = new byte[len];
                    rnd.nextBytes(raw);
                    BidiConversionProperties p = new BidiConversionProperties(type);
                    p.setBidiImplicitReordering(implicit);
                    p.setBidiNumericOrderingRoundTrip(numeric);
                    String decoded = table.byteArrayToString(raw, 0, len, p);
                    out.println("C\t" + ccsid + "\t" + dsn + "\t" + cfg[1] + "\t" + cfg[2] + "\tD\t"
                            + hexBytes(raw, len) + "\t" + hexString(decoded));
                    lines++;

                    int elen = rnd.nextInt(31);
                    StringBuilder sb = new StringBuilder();
                    for (int i = 0; i < elen; i++) {
                        if (rnd.nextInt(10) == 0) sb.append(' ');
                        else sb.append(chars.charAt(rnd.nextInt(chars.length())));
                    }
                    String text = sb.toString();
                    BidiConversionProperties q = new BidiConversionProperties(type);
                    q.setBidiImplicitReordering(implicit);
                    q.setBidiNumericOrderingRoundTrip(numeric);
                    byte[] encoded = table.stringToByteArray(text, q);
                    out.println("C\t" + ccsid + "\t" + dsn + "\t" + cfg[1] + "\t" + cfg[2] + "\tE\t"
                            + hexString(text) + "\t" + hexBytes(encoded, encoded.length));
                    lines++;
                }
                configs++;
            }
        }
        System.err.println("conv vectors: " + lines + " (configurations: " + configs + ")");
    }

    static final String STR_ALPHABET = "ABCxyz abc 0123 ()[]{}.,:;-+/*=<>!?'\"\n\r\t"
            + "אבגדהוזחטיךכלםמןנ"
            + "ابتثجرزلمنهوىي٠١٢"
            + "ﺍﺎﻻﻼـًَ";

    static String randomString(Random rnd, int maxLen) {
        int len = rnd.nextInt(maxLen + 1);
        StringBuilder sb = new StringBuilder();
        for (int i = 0; i < len; i++) sb.append(STR_ALPHABET.charAt(rnd.nextInt(STR_ALPHABET.length())));
        return sb.toString();
    }

    static void strings(PrintWriter out) {
        Random rnd = new Random(20260615L);
        int[] types = { -1, 0, 4, 5, 6, 7, 8, 9, 10, 11 };
        int lines = 0;
        for (int in : types) {
            for (int to : types) {
                for (int n = 0; n < 12; n++) {
                    String s = randomString(rnd, 40);
                    out.println("T\t" + in + "\t" + to + "\t" + hexString(s) + "\t"
                            + hexString(AS400BidiTransform.bidiTransform(s, in, to)));
                    String q = randomString(rnd, 60);
                    out.println("S\t" + in + "\t" + to + "\t" + hexString(q) + "\t"
                            + hexString(AS400BidiTransform.SQL_statement_reordering(q, in, to)));
                    String m = randomString(rnd, 60);
                    out.println("M\t" + in + "\t" + to + "\t" + hexString(m) + "\t"
                            + hexString(AS400BidiTransform.meta_data_reordering(m, in, to)));
                    lines += 3;
                }
            }
        }
        System.err.println("string vectors: " + lines);
    }
}
