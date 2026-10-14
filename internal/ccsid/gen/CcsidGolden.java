package com.ibm.as400.access;

import java.io.*;
import java.nio.charset.StandardCharsets;
import java.util.*;

// Writes golden conversion vectors produced by JTOpen.
// Line format: E|D <ccsid> <hex utf8 input|hex bytes input> <hex output>
public class CcsidGolden {
    static String hex(byte[] b) {
        StringBuilder sb = new StringBuilder();
        for (byte x : b) sb.append(String.format("%02x", x & 0xff));
        return sb.length() == 0 ? "-" : sb.toString();
    }
    // Mirrors Go's utf16.Decode: unpaired surrogates become U+FFFD.
    static String clean(String s) {
        StringBuilder sb = new StringBuilder();
        for (int i = 0; i < s.length(); i++) {
            char c = s.charAt(i);
            if (Character.isHighSurrogate(c) && i + 1 < s.length() && Character.isLowSurrogate(s.charAt(i + 1))) {
                sb.append(c).append(s.charAt(++i));
            } else if (Character.isSurrogate(c)) {
                sb.append('�');
            } else {
                sb.append(c);
            }
        }
        return sb.toString();
    }
    public static void main(String[] args) throws Exception {
        String[] samples = {
            "ABC abc 0123 !?#@$",
            "Grüße äöüß €100 ½ ©",
            "日本語テキスト ｶﾀｶﾅ ひらがな",
            "中文简体 繁體中文 ‾",
            "한국어 텍스트",
            "mixed ABC 日本 xyz 中 end",
            "\uD840\uDC0B\uD844\uDE3D x \uD842\uDFB7",
            "\u304B\u309A \u31F7\u309A \u02E9\u02E5",
            "\uFFFD\u001A tab\tnl\n",
            "Привет Ελληνικά עברית العربية ไทย",
        };
        Random rnd = new Random(400);
        PrintWriter w = new PrintWriter(new OutputStreamWriter(new FileOutputStream(args[0]), StandardCharsets.UTF_8));
        for (int a = 1; a < args.length; a++) {
            int ccsid = Integer.parseInt(args[a]);
            ConvTable t = ConvTable.getTable(ccsid, null);
            boolean bidi = t instanceof ConvTableBidiMap;
            // Bidi CCSIDs are converted character by character (no layout transform).
            BidiConversionProperties props = new BidiConversionProperties();
            props.setBidiStringType(BidiStringType.NONE);
            for (String s : samples) {
                
                byte[] enc = t.stringToByteArray(s, props);
                w.println("E " + ccsid + " " + hex(s.getBytes(StandardCharsets.UTF_8)) + " " + hex(enc));
                String dec = t.byteArrayToString(enc, 0, enc.length, props);
                w.println("D " + ccsid + " " + hex(enc) + " " + hex(clean(dec).getBytes(StandardCharsets.UTF_8)));
            }
            if (bidi || t instanceof ConvTableUnicodeBigMap || ccsid == 1202 || ccsid == 1208) continue;
            for (int k = 0; k < 4; k++) {
                byte[] b = new byte[64];
                rnd.nextBytes(b);
                if (t instanceof ConvTableMixedMap) {
                    for (int i = 0; i < b.length; i++) if (b[i] == 0x0E || b[i] == 0x0F) b[i] = 0x40;
                    b[10] = 0x0E; b[31] = 0x0F; b[40] = 0x0E;
                    if (k == 3) { b[63] = 0x0F; }
                }
                String dec = t.byteArrayToString(b, 0, b.length, props);
                w.println("D " + ccsid + " " + hex(b) + " " + hex(clean(dec).getBytes(StandardCharsets.UTF_8)));
            }
        }
        w.close();
    }
}
