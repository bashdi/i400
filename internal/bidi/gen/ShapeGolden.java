package com.ibm.as400.access;

import java.io.PrintWriter;
import java.lang.reflect.Array;
import java.lang.reflect.Field;
import java.util.Random;

/**
 * Golden-vector and table generator for the Go port internal/bidi (BidiShape, ArabicOptionSet, BidiFlagSet).
 *
 * Usage:
 *   java -cp classes com.ibm.as400.access.ShapeGolden tables <out.go>
 *   java -cp classes com.ibm.as400.access.ShapeGolden golden <out.txt>
 *
 * "tables" dumps the private static tables of BidiShape as Go source (tables.go).
 * "golden" writes one vector per line, tab separated:
 *   id, inAttr chars, outAttr chars, arabic option chars, input hex, output hex | EXC:<exception>
 * Hex is 4 hex digits per UTF-16 code unit.
 */
public class ShapeGolden {

    private static final int[] ARABIC = range(0x0621, 0x064A);
    private static final int[] PRES = range(0xFE70, 0xFEF4);
    private static final int[] LAMALEF = range(0xFEF5, 0xFEFC);
    private static final int[] LAM_ALEF_BASE = {0x0644, 0x0622, 0x0623, 0x0625, 0x0627};
    private static final int[] TASHKEEL06 = range(0x064B, 0x0652);
    private static final int[] TASHKEEL_FE = {0xFE70, 0xFE71, 0xFE72, 0xFE74, 0xFE76, 0xFE77, 0xFE78, 0xFE79,
                                              0xFE7A, 0xFE7B, 0xFE7C, 0xFE7D, 0xFE7E, 0xFE7F};
    private static final int[] SEEN = {0xFEB1, 0xFEB2, 0xFEB5, 0xFEB6, 0xFEB9, 0xFEBA, 0xFEBD, 0xFEBE};
    private static final int[] YEH_HAMZA = {0xFE89, 0xFE8A, 0xFE80, 0xFEEF, 0xFEF0, 0x0626, 0x0624};
    private static final int[] MISC = {0x200B, 0x200D, 0x206D, 0x206E, 0x206F, 0x0640,
                                       0x065C, 0x065D, 0x065E, 0x065F, 0x0660, 0x0661, 0x0669};
    private static final int[] LATIN = concat(range(0x41, 0x5A), range(0x61, 0x7A));
    private static final int[] DIGITS = range(0x30, 0x39);
    private static final int[] EXT06 = range(0x0622, 0x06D3);

    public static void main(String[] args) throws Exception {
        if (args.length != 2) {
            System.err.println("usage: ShapeGolden tables|golden <outfile>");
            System.exit(2);
        }
        if (args[0].equals("tables")) {
            dumpTables(args[1]);
        } else if (args[0].equals("golden")) {
            writeGolden(args[1]);
        } else {
            System.err.println("unknown mode " + args[0]);
            System.exit(2);
        }
    }

    // ------------------------------------------------------------------ tables

    private static Object field(String name) throws Exception {
        Field f = BidiShape.class.getDeclaredField(name);
        f.setAccessible(true);
        return f.get(null);
    }

    private static void dumpTables(String out) throws Exception {
        StringBuilder sb = new StringBuilder();
        sb.append("// Code generated from JTOpen BidiShape.java static tables by\n");
        sb.append("// internal/bidi/gen/ShapeGolden.java (mode \"tables\"). DO NOT EDIT.\n\n");
        sb.append("package bidi\n\n");
        sb.append("// Tables from BidiShape.java. Go names are the Java names, lower-camel-cased.\n\n");
        // java field, go name, go element type
        String[][] specs = {
            {"shapeTable", "shapeTable", "int16"},
            {"convertFEto06", "convertFEto06", "int32"},
            {"convert06toFE", "convert06toFE", "int32"},
            {"Link06", "link06", "int32"},
            {"LinkFE", "linkFE", "int16"},
            {"IrreleventPos", "irrelevantPos", "int16"},
            {"AlefType", "alefType", "uint16"},
            {"Tashkeel", "tashkeel", "uint16"},
            {"Tashkeel_Tatweel", "tashkeelTatweel", "uint16"},
        };
        for (String[] s : specs) {
            Object arr = field(s[0]);
            String goType = typeOf(arr, s[2]);
            sb.append("var ").append(s[1]).append(" = ").append(goType).append("{");
            appendArray(sb, arr);
            sb.append("\n}\n\n");
        }
        try (PrintWriter pw = new PrintWriter(out, "UTF-8")) {
            pw.print(sb);
        }
    }

    // Type of a rectangular (non-jagged) Java array as a Go array type.
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

    // ------------------------------------------------------------------ golden

    private static void writeGolden(String out) throws Exception {
        String[] inTypes = {"I", "V"};
        String[] inOrient = {"L", "R"};
        String[] inSwap = {"Y", "N"};
        String[] text6 = {"N", "S", "I", "M", "F", "B"};
        String[] numerals = {"N", "H", "C", "A"};
        String[] outType = {"I", "V"};
        String[] outOrient = {"L", "R", "C", "D"};
        String[] outSwap = {"Y", "N"};
        String[] lamOpts = {"R", "N", "B", "E", "A"};
        String[] seenOpts = {"N", "B", "E", "A"};
        String[] yehOpts = {"O", "N", "B", "E", "A"};
        String[] tashOpts = {"K", "Z", "W", "B", "E", "A"};

        Random rnd = new Random(20260614L);
        int id = 0;
        int exceptions = 0;
        StringBuilder sb = new StringBuilder();
        sb.append("# ShapeGolden vectors: id, inAttr, outAttr, arabicOpts, inputHex, outputHex|EXC\n");
        sb.append("# Generated by internal/bidi/gen/ShapeGolden.java (mode golden). Hex = 4 digits per UTF-16 unit.\n");
        BidiShape shape = new BidiShape();
        for (String lam : lamOpts) for (String seen : seenOpts) for (String yeh : yehOpts) for (String tash : tashOpts) {
            String opts = "000000" + lam + seen + yeh + tash;
            ArabicOptionSet optSet = new ArabicOptionSet(opts.toCharArray());
            for (int k = 0; k < 8; k++) {
                String inA = pick(rnd, inTypes) + pick(rnd, inOrient) + pick(rnd, inSwap)
                           + pick(rnd, text6) + pick(rnd, numerals);
                String outA = pick(rnd, outType) + pick(rnd, outOrient) + pick(rnd, outSwap)
                            + pick(rnd, text6) + pick(rnd, numerals);
                // Force the shaping branches: visual->implicit (deshape), implicit->visual (shape).
                if (k < 3) inA = "V" + inA.substring(1);
                if (k >= 3 && k < 6) outA = "V" + outA.substring(1);
                if (k == 6) { inA = "I" + inA.substring(1); outA = "I" + outA.substring(1); }
                if (k == 7) { inA = "V" + inA.substring(1); outA = "V" + outA.substring(1); }
                char[] text = randomText(rnd);
                BidiFlagSet inF = new BidiFlagSet(inA.toCharArray());
                BidiFlagSet outF = new BidiFlagSet(outA.toCharArray());
                String inHex = hex(text);
                String outHex;
                try {
                    char[] res = shape.shape(inF, outF, text.clone(), optSet);
                    outHex = hex(res);
                } catch (RuntimeException e) {
                    outHex = "EXC:" + e.getClass().getSimpleName();
                    exceptions++;
                }
                sb.append(id++).append('\t').append(inA).append('\t').append(outA).append('\t')
                  .append(opts).append('\t').append(inHex).append('\t').append(outHex).append('\n');
            }
        }
        try (PrintWriter pw = new PrintWriter(out, "UTF-8")) {
            pw.print(sb);
        }
        System.out.println("vectors=" + id + " exceptions=" + exceptions);
    }

    private static String pick(Random rnd, String[] a) {
        return a[rnd.nextInt(a.length)];
    }

    private static char[] randomText(Random rnd) {
        int len = rnd.nextInt(10) == 0 ? 0 : 1 + rnd.nextInt(36);
        char[] out = new char[len];
        for (int i = 0; i < len; i++) {
            int r = rnd.nextInt(100);
            int cp;
            if (r < 30) cp = ARABIC[rnd.nextInt(ARABIC.length)];
            else if (r < 40) cp = PRES[rnd.nextInt(PRES.length)];
            else if (r < 50) cp = LAMALEF[rnd.nextInt(LAMALEF.length)];
            else if (r < 60) cp = LAM_ALEF_BASE[rnd.nextInt(LAM_ALEF_BASE.length)];
            else if (r < 65) cp = TASHKEEL06[rnd.nextInt(TASHKEEL06.length)];
            else if (r < 71) cp = TASHKEEL_FE[rnd.nextInt(TASHKEEL_FE.length)];
            else if (r < 76) cp = SEEN[rnd.nextInt(SEEN.length)];
            else if (r < 80) cp = YEH_HAMZA[rnd.nextInt(YEH_HAMZA.length)];
            else if (r < 88) cp = 0x20;
            else if (r < 91) cp = LATIN[rnd.nextInt(LATIN.length)];
            else if (r < 94) cp = DIGITS[rnd.nextInt(DIGITS.length)];
            else if (r < 96) cp = 0x0660 + rnd.nextInt(10);
            else if (r < 98) cp = EXT06[rnd.nextInt(EXT06.length)];
            else cp = MISC[rnd.nextInt(MISC.length)];
            out[i] = (char) cp;
        }
        return out;
    }

    private static String hex(char[] c) {
        StringBuilder sb = new StringBuilder(c.length * 4);
        for (char ch : c) sb.append(String.format("%04X", (int) ch));
        return sb.toString();
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
