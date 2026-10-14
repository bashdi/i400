package com.ibm.as400.access;

import java.io.FileOutputStream;
import java.io.OutputStreamWriter;
import java.io.PrintWriter;
import java.io.Writer;
import java.lang.reflect.Array;
import java.lang.reflect.Field;
import java.util.Arrays;
import java.util.Random;
import java.util.zip.GZIPOutputStream;

/**
 * Table and golden-vector generator for the Go port of BidiOrder, BidiText, BidiTransform,
 * BidiConversionProperties and AS400BidiTransform (internal/bidi).
 *
 * Usage (compile and run as in ShapeGolden, classes in ./classes):
 *   java -cp classes com.ibm.as400.access.OrderGolden tables    <out order_tables.go>
 *   java -cp classes com.ibm.as400.access.OrderGolden order     <out order_golden.txt.gz>
 *   java -cp classes com.ibm.as400.access.OrderGolden transform <out transform_golden.txt.gz>
 *
 * "tables" dumps the private static tables of BidiOrder (notSpacing, symPairs, impTab_*) by reflection.
 *
 * "order" writes one BidiText.transform(BidiTransform) vector per line, tab separated:
 *   id, inFlags, outFlags, bits, delimiters, arabic, inHex, result
 *   bits: impToImp roundTrip winCompatible insertMarkers removeMarkers wordBreak
 *         srcToDstMap dstToSrcMap propertyMap  (1 = set)
 *   delimiters: "-" for null, else hex; arabic: "-" for null, else 10 option chars.
 *   result: "EXC:<name>" or  outHex|s2dHex|d2sHex|pmHex|inpCount|outCount
 *           (the three map fields are empty when the map was not requested).
 *
 * "transform" writes AS400BidiTransform vectors (toJavaLayout / toAS400Layout, i.e. the sequence used by
 * ConvTableBidiMap.byteArrayToCharArray / charArrayToByteArray after the table mapping), tab separated:
 *   id, dir (J = toJavaLayout, A = toAS400Layout), ccsid, stringType, impToImp, numericRoundTrip,
 *   removeMarksOnImplicitToVisual, inHex, outHex
 *   outHex is the complete returned data array (its length may exceed the count of valid characters, as in JTOpen).
 *
 * Before writing the transform vectors the generator checks, for CCSIDs that have a ConvTableBidiMap class,
 * that the real table path (ConvTableBidiMap.byteArrayToCharArray / charArrayToByteArray) equals the
 * AS400BidiTransform sequence used for the vectors. A mismatch aborts generation.
 */
public class OrderGolden {

    private static final int[] DIGIT_ASCII = range(0x30, 0x39);
    private static final int[] HEBREW = range(0x05D0, 0x05EA);
    private static final int[] ARABIC = range(0x0627, 0x064A);
    private static final int[] PRESENTATION = range(0xFE70, 0xFEFC);
    private static final int[] LATIN = concat(range(0x41, 0x5A), range(0x61, 0x7A));
    private static final int[] AN = range(0x0660, 0x0669);
    private static final int[] EXT_EN = range(0x06F0, 0x06F9);
    private static final int[] SYMS = {0x24, 0x25, 0x23, 0x2B, 0x2D, 0x2F, 0x2C, 0x2E, 0x3A,
                                       0x28, 0x29, 0x5B, 0x5D, 0x7B, 0x7D, 0x3C, 0x3E, 0xAB, 0xBB,
                                       0x21, 0x3F, 0x2A, 0x26, 0x40, 0x22, 0x7C, 0x0A,
                                       0x2029, 0x3000, 0x200E, 0x200F, 0x202A, 0x202B, 0x202C, 0x202D,
                                       0x202E, 0x064B, 0x05B0, 0x0300, 0x0622, 0x0644, 0x0627, 0x0640,
                                       0x060C, 0x00A0, 0x00B0, 0x2000};

    private static final String[] IN_TYPES = {"I", "V"};
    private static final String[] ORIENT = {"L", "R", "C", "D"};
    private static final String[] SWAP = {"Y", "N"};
    private static final String[] TEXT = {"N", "S", "I", "M", "F", "B"};
    private static final String[] NUMERALS = {"N", "H", "C", "A"};
    private static final String[] LAM = {"R", "N", "B", "E", "A"};
    private static final String[] SEEN = {"N", "B", "E", "A"};
    private static final String[] YEH = {"O", "N", "B", "E", "A"};
    private static final String[] TASH = {"K", "Z", "W", "B", "E", "A"};
    private static final String[] DELIMS = {null, "", "|", "\"\"", "|\"", "()", "\"|\""};

    private static final int[] CCSIDS = {420, 424, 425, 856, 862, 864, 916, 1046, 1089, 1255, 1256,
                                         5351, 8612, 12708, 62211, 62224, 62235, 62245, 62251};
    private static final int[] STRING_TYPES = {-1, 0, 4, 5, 6, 7, 8, 9, 10, 11};

    public static void main(String[] args) throws Exception {
        if (args.length != 2) {
            System.err.println("usage: OrderGolden tables|order|transform <outfile>");
            System.exit(2);
        }
        switch (args[0]) {
            case "tables": dumpTables(args[1]); break;
            case "order": writeOrder(args[1]); break;
            case "transform": writeTransform(args[1]); break;
            default:
                System.err.println("unknown mode " + args[0]);
                System.exit(2);
        }
    }

    // ------------------------------------------------------------------ tables

    private static Object field(Class<?> c, String name) throws Exception {
        Field f = c.getDeclaredField(name);
        f.setAccessible(true);
        return f.get(null);
    }

    private static void dumpTables(String out) throws Exception {
        StringBuilder sb = new StringBuilder();
        sb.append("// Code generated from JTOpen BidiOrder.java static tables by\n");
        sb.append("// internal/bidi/gen/OrderGolden.java (mode \"tables\"). DO NOT EDIT.\n\n");
        sb.append("package bidi\n\n");
        sb.append("// Tables from BidiOrder.java. Go names are the Java names, lower-camel-cased.\n\n");
        // AS400BidiTransform.CCSID_TABLE: CCSID and string type, sorted by CCSID.
        Object ccsidArr = field(AS400BidiTransform.class, "CCSID_TABLE");
        sb.append("var ccsidTable = ").append(typeOf(ccsidArr, "int")).append("{");
        appendArray(sb, ccsidArr);
        sb.append("\n}\n\n");

        String[][] specs = {
            {"notSpacing", "notSpacing", "uint16"},
            {"symPairs", "symPairs", "uint16"},
            {"impTab_LTR", "impTabLTR", "int16"},
            {"impTab_RTL", "impTabRTL", "int16"},
            {"impTab_LTR_r", "impTabLTRr", "int16"},
            {"impTab_RTL_r", "impTabRTLr", "int16"},
            {"impTab_LTR_w", "impTabLTRw", "int16"},
            {"impTab_LTR_m", "impTabLTRm", "int16"},
            {"impTab_RTL_m", "impTabRTLm", "int16"},
            {"impTab_LTR_wm", "impTabLTRwm", "int16"},
        };
        for (String[] s : specs) {
            Object arr = field(BidiOrder.class, s[0]);
            sb.append("var ").append(s[1]).append(" = ").append(typeOf(arr, s[2])).append("{");
            appendArray(sb, arr);
            sb.append("\n}\n\n");
        }
        try (PrintWriter pw = new PrintWriter(out, "UTF-8")) {
            pw.print(sb);
        }
    }

    private static String typeOf(Object arr, String elem) {
        StringBuilder t = new StringBuilder();
        Object cur = arr;
        while (cur.getClass().isArray()) {
            t.append("[").append(Array.getLength(cur)).append("]");
            if (Array.getLength(cur) == 0) break;
            cur = Array.get(cur, 0);
        }
        return t.append(elem).toString();
    }

    private static void appendArray(StringBuilder sb, Object arr) {
        int n = Array.getLength(arr);
        boolean nested = n > 0 && Array.get(arr, 0).getClass().isArray();
        if (nested) {
            for (int i = 0; i < n; i++) {
                sb.append("\n\t{");
                appendInline(sb, Array.get(arr, i));
                sb.append("},");
            }
        } else {
            for (int i = 0; i < n; i++) {
                if (i % 16 == 0) sb.append("\n\t");
                sb.append(numeric(Array.get(arr, i))).append(", ");
            }
        }
    }

    private static void appendInline(StringBuilder sb, Object arr) {
        int n = Array.getLength(arr);
        boolean nested = n > 0 && Array.get(arr, 0).getClass().isArray();
        for (int i = 0; i < n; i++) {
            if (nested) {
                sb.append("{");
                appendInline(sb, Array.get(arr, i));
                sb.append("}, ");
            } else {
                sb.append(numeric(Array.get(arr, i))).append(", ");
            }
        }
    }

    private static String numeric(Object v) {
        if (v instanceof Character) return Integer.toString((int) ((Character) v).charValue());
        return v.toString();
    }

    // ------------------------------------------------------------------ writers

    private static Writer gzWriter(String out) throws Exception {
        return new OutputStreamWriter(new GZIPOutputStream(new FileOutputStream(out)), "UTF-8");
    }

    private static void writeOrder(String out) throws Exception {
        Random rnd = new Random(20260701L);
        int n = 12000;
        int exceptions = 0;
        try (Writer w = gzWriter(out)) {
            w.write("# OrderGolden order vectors (BidiText.transform(BidiTransform): reorder + shape).\n");
            w.write("# Generated by internal/bidi/gen/OrderGolden.java (mode order). See the class comment for the format.\n");
            for (int id = 0; id < n; id++) {
                String inA = pick(rnd, IN_TYPES) + pick(rnd, ORIENT) + pick(rnd, SWAP)
                           + pick(rnd, TEXT) + pick(rnd, NUMERALS);
                String outA = pick(rnd, IN_TYPES) + pick(rnd, ORIENT) + pick(rnd, SWAP)
                            + pick(rnd, TEXT) + pick(rnd, NUMERALS);
                if (id % 4 == 0) inA = "V" + inA.substring(1);   // visual -> implicit paths
                if (id % 4 == 1) outA = "V" + outA.substring(1);
                if (id % 4 == 2) { inA = "I" + inA.substring(1); outA = "I" + outA.substring(1); }
                if (id % 9 == 3) inA = inA.substring(0, 1) + "C" + inA.substring(2);
                String delim = pick(rnd, DELIMS);
                String arab = rnd.nextInt(10) < 3 ? null
                        : "000000" + pick(rnd, LAM) + pick(rnd, SEEN) + pick(rnd, YEH) + pick(rnd, TASH);
                boolean[] bits = new boolean[9];
                bits[0] = rnd.nextInt(10) < 8;   // impToImp (Java default true)
                bits[1] = rnd.nextInt(5) == 0;   // roundTrip
                bits[2] = rnd.nextInt(5) == 0;   // winCompatible
                bits[3] = rnd.nextInt(4) == 0;   // insertMarkers
                bits[4] = rnd.nextInt(5) == 0;   // removeMarkers
                bits[5] = rnd.nextInt(5) == 0;   // wordBreak
                bits[6] = rnd.nextInt(3) == 0;   // srcToDstMap
                bits[7] = rnd.nextInt(3) == 0;   // dstToSrcMap
                bits[8] = rnd.nextInt(3) == 0;   // propertyMap
                char[] text = randomText(rnd);

                BidiFlagSet inF = new BidiFlagSet(inA.toCharArray());
                BidiFlagSet outF = new BidiFlagSet(outA.toCharArray());
                BidiTransform bdx = new BidiTransform();
                bdx.flags.setAllFlags(outF);
                bdx.impToImp = bits[0];
                bdx.roundTrip = bits[1];
                bdx.winCompatible = bits[2];
                bdx.insertMarkers = bits[3];
                bdx.removeMarkers = bits[4];
                bdx.wordBreak = bits[5];
                bdx.srcToDstMapRequired = bits[6];
                bdx.dstToSrcMapRequired = bits[7];
                bdx.propertyMapRequired = bits[8];
                if (delim != null) bdx.delimiters = delim;
                if (arab != null) bdx.options = new ArabicOptionSet(arab.toCharArray());

                String result;
                try {
                    BidiText src = new BidiText(inF, text);
                    BidiText dst = src.transform(bdx);
                    StringBuilder r = new StringBuilder();
                    r.append(hex(dst.data, dst.offset, dst.count)).append('|');
                    if (bits[6]) r.append(intsHex(bdx.srcToDstMap, bdx.inpCount));
                    r.append('|');
                    if (bits[7]) r.append(intsHex(bdx.dstToSrcMap, bdx.outCount));
                    r.append('|');
                    if (bits[8]) r.append(bytesHex(bdx.propertyMap, bdx.inpCount));
                    r.append('|').append(bdx.inpCount).append('|').append(bdx.outCount);
                    result = r.toString();
                } catch (RuntimeException e) {
                    result = "EXC:" + e.getClass().getSimpleName();
                    exceptions++;
                }
                StringBuilder bitStr = new StringBuilder();
                for (boolean b : bits) bitStr.append(b ? '1' : '0');
                w.write(id + "\t" + inA + "\t" + outA + "\t" + bitStr + "\t"
                        + (delim == null ? "-" : hex(delim.toCharArray(), 0, delim.length())) + "\t"
                        + (arab == null ? "-" : arab) + "\t" + hex(text, 0, text.length) + "\t" + result + "\n");
            }
        }
        System.out.println("order vectors=" + n + " exceptions=" + exceptions);
    }

    private static void writeTransform(String out) throws Exception {
        Random rnd = new Random(20260702L);
        int lines = 0;
        try (Writer w = gzWriter(out)) {
            w.write("# OrderGolden transform vectors (AS400BidiTransform toJavaLayout / toAS400Layout).\n");
            w.write("# Generated by internal/bidi/gen/OrderGolden.java (mode transform). See the class comment for the format.\n");
            for (int ccsid : CCSIDS) {
                checkTablePath(ccsid, rnd);
                for (int st : STRING_TYPES) {
                    for (int combo = 0; combo < 8; combo++) {
                        boolean impl = (combo & 1) == 0;
                        boolean num = (combo & 2) != 0;
                        boolean rm = (combo & 4) != 0;
                        BidiConversionProperties p = props(st, impl, num, rm);
                        for (int k = 0; k < 4; k++) {
                            char[] text = randomText(rnd);
                            String id = Integer.toString(lines);
                            String common = "\t" + ccsid + "\t" + st + "\t" + (impl ? 1 : 0) + "\t" + (num ? 1 : 0)
                                    + "\t" + (rm ? 1 : 0) + "\t" + hex(text, 0, text.length) + "\t";
                            w.write(id + "\tJ" + common + hexFull(javaPath(text, ccsid, p)) + "\n");
                            lines++;
                            id = Integer.toString(lines);
                            w.write(id + "\tA" + common + hexFull(hostPath(text, ccsid, p)) + "\n");
                            lines++;
                        }
                    }
                }
            }
        }
        System.out.println("transform vectors=" + lines);
    }

    private static BidiConversionProperties props(int st, boolean impl, boolean num, boolean rm) {
        BidiConversionProperties p = new BidiConversionProperties();
        p.setBidiStringType(st);
        p.setBidiImplicitReordering(impl);
        p.setBidiNumericOrderingRoundTrip(num);
        p.setBidiRemoveMarksOnImplicitToVisual(rm);
        return p;
    }

    /** Exactly the post-table part of ConvTableBidiMap.byteArrayToCharArray. */
    static char[] javaPath(char[] decoded, int ccsid, BidiConversionProperties p) {
        int type = p.getBidiStringType();
        char[] dest = decoded.clone();
        if (type == BidiStringType.NONE) return dest;
        AS400BidiTransform abt = new AS400BidiTransform(ccsid);
        abt.setBidiConversionProperties(p);
        abt.setJavaStringType(p.getBidiStringType());
        abt.setAS400StringType(AS400BidiTransform.getStringType(ccsid));
        return abt.toJavaLayout(dest);
    }

    /** Exactly the transform part of ConvTableBidiMap.charArrayToByteArray (before the table mapping). */
    static char[] hostPath(char[] javaText, int ccsid, BidiConversionProperties p) {
        AS400BidiTransform abt = new AS400BidiTransform(ccsid);
        abt.setBidiConversionProperties(p);
        abt.setJavaStringType(p.getBidiStringType());
        abt.setAS400StringType(AS400BidiTransform.getStringType(ccsid));
        return abt.toAS400Layout(javaText.clone());
    }

    /** For CCSIDs with a ConvTableBidiMap class, the real table path must equal the sequence above. */
    private static void checkTablePath(int ccsid, Random rnd) throws Exception {
        ConvTable t = ConvTable.getTable(ccsid, null);
        if (!(t instanceof ConvTableBidiMap)) {
            System.out.println("no ConvTableBidiMap for ccsid " + ccsid + ": table path not checked");
            return;
        }
        ConvTableBidiMap m = (ConvTableBidiMap) t;
        for (int st : STRING_TYPES) {
            for (int combo = 0; combo < 8; combo++) {
                BidiConversionProperties p = props(st, (combo & 1) == 0, (combo & 2) != 0, (combo & 4) != 0);
                for (int k = 0; k < 3; k++) {
                    byte[] bytes = new byte[1 + rnd.nextInt(30)];
                    for (int i = 0; i < bytes.length; i++) bytes[i] = (byte) rnd.nextInt(256);
                    char[] decoded = new char[bytes.length];
                    for (int i = 0; i < bytes.length; i++) decoded[i] = m.toUnicode_[0x00FF & bytes[i]];
                    char[] viaTable = m.byteArrayToCharArray(bytes, 0, bytes.length, p);
                    if (!Arrays.equals(viaTable, javaPath(decoded, ccsid, p)))
                        throw new IllegalStateException("table path differs from sequence, ccsid " + ccsid + " st " + st);
                    char[] text = randomText(rnd);
                    byte[] viaTableHost = m.charArrayToByteArray(text, p);
                    char[] host = hostPath(text, ccsid, p);
                    byte[] expect = new byte[host.length];
                    for (int i = 0; i < host.length; i++) expect[i] = m.fromUnicode_[host[i]];
                    if (!Arrays.equals(viaTableHost, expect))
                        throw new IllegalStateException("host table path differs, ccsid " + ccsid + " st " + st);
                }
            }
        }
        System.out.println("table path verified for ccsid " + ccsid);
    }

    // ------------------------------------------------------------------ random text

    private static char[] randomText(Random rnd) {
        int len = rnd.nextInt(12) == 0 ? 0 : 1 + rnd.nextInt(30);
        char[] out = new char[len];
        for (int i = 0; i < len; i++) {
            int r = rnd.nextInt(100);
            int cp;
            if (r < 22) cp = HEBREW[rnd.nextInt(HEBREW.length)];
            else if (r < 42) cp = ARABIC[rnd.nextInt(ARABIC.length)];
            else if (r < 50) cp = PRESENTATION[rnd.nextInt(PRESENTATION.length)];
            else if (r < 60) cp = LATIN[rnd.nextInt(LATIN.length)];
            else if (r < 68) cp = DIGIT_ASCII[rnd.nextInt(DIGIT_ASCII.length)];
            else if (r < 72) cp = AN[rnd.nextInt(AN.length)];
            else if (r < 74) cp = EXT_EN[rnd.nextInt(EXT_EN.length)];
            else if (r < 77) cp = 0x20;
            else cp = SYMS[rnd.nextInt(SYMS.length)];
            out[i] = (char) cp;
        }
        return out;
    }

    // ------------------------------------------------------------------ helpers

    private static String hex(char[] c, int off, int n) {
        StringBuilder sb = new StringBuilder(n * 4);
        for (int i = 0; i < n; i++) sb.append(String.format("%04X", (int) c[off + i]));
        return sb.toString();
    }

    private static String hexFull(char[] c) {
        return hex(c, 0, c.length);
    }

    private static String intsHex(int[] a, int n) {
        StringBuilder sb = new StringBuilder(n * 8);
        for (int i = 0; i < n; i++) sb.append(String.format("%08X", a[i]));
        return sb.toString();
    }

    private static String bytesHex(byte[] a, int n) {
        StringBuilder sb = new StringBuilder(n * 2);
        for (int i = 0; i < n; i++) sb.append(String.format("%02X", a[i] & 0xFF));
        return sb.toString();
    }

    private static String pick(Random rnd, String[] a) {
        return a[rnd.nextInt(a.length)];
    }

    private static int[] range(int from, int to) {
        int[] r = new int[to - from + 1];
        for (int i = 0; i < r.length; i++) r[i] = from + i;
        return r;
    }

    private static int[] concat(int[] a, int[] b) {
        int[] r = new int[a.length + b.length];
        System.arraycopy(a, 0, r, 0, a.length);
        System.arraycopy(b, 0, r, a.length, b.length);
        return r;
    }
}
