package com.ibm.as400.access;

import java.io.*;
import java.util.*;
import java.util.zip.*;

// Dumps JTOpen conversion tables into a compact binary format consumed by
// the Go driver (internal/ccsid). Usage: CcsidDump <outDir> <ccsid>...
public class CcsidDump {
    public static void main(String[] args) throws Exception {
        File out = new File(args[0]);
        out.mkdirs();
        StringBuilder index = new StringBuilder();
        for (int a = 1; a < args.length; a++) {
            int ccsid = Integer.parseInt(args[a]);
            ConvTable t;
            try {
                t = ConvTable.getTable(ccsid, null);
            } catch (Throwable e) {
                System.err.println("skip " + ccsid + ": " + e);
                continue;
            }
            ByteArrayOutputStream raw = new ByteArrayOutputStream();
            DataOutputStream d = new DataOutputStream(raw);
            String kind;
            if (t instanceof ConvTableMixedMap) {
                ConvTableMixedMap m = (ConvTableMixedMap) t;
                kind = "M";
                d.writeByte('M');
                d.writeInt(m.sbTable_.getCcsid());
                d.writeInt(m.dbTable_.getCcsid());
            } else if (t instanceof ConvTableDoubleMap) {
                ConvTableDoubleMap m = (ConvTableDoubleMap) t;
                kind = "D";
                d.writeByte('D');
                char[] toU = m.getToUnicode();
                char[] fromU = m.getFromUnicode();
                List<int[]> multi = new ArrayList<>();
                char[] dest = new char[8];
                for (int i = 0; i < 65536; i++) {
                    char c = i < toU.length ? toU[i] : '�';
                    if (c >= 0xD800 && c <= 0xD80F) {
                        int n = m.toUnicode(dest, 0, i);
                        if (n == 1) {
                            c = dest[0];
                        } else {
                            int[] e = new int[n + 1];
                            e[0] = i;
                            for (int k = 0; k < n; k++) e[k + 1] = dest[k];
                            multi.add(e);
                            c = '￿';
                        }
                    }
                    d.writeChar(c);
                }
                for (int i = 0; i < 65536; i++) {
                    d.writeChar(i < fromU.length ? fromU[i] : '﻾');
                }
                d.writeInt(multi.size());
                for (int[] e : multi) {
                    d.writeChar(e[0]);
                    d.writeByte(e.length - 1);
                    for (int k = 1; k < e.length; k++) d.writeChar(e[k]);
                }
            } else {
                char[] toU;
                byte[] fromU;
                byte sub;
                if (t instanceof ConvTableSingleMap) {
                    toU = ((ConvTableSingleMap) t).toUnicode_;
                    fromU = ((ConvTableSingleMap) t).fromUnicode_;
                    sub = 0x3F;
                    kind = "S";
                } else if (t instanceof ConvTableBidiMap) {
                    toU = ((ConvTableBidiMap) t).toUnicode_;
                    fromU = ((ConvTableBidiMap) t).fromUnicode_;
                    sub = 0x3F;
                    kind = "B";
                } else if (t instanceof ConvTableAsciiMap) {
                    toU = ((ConvTableAsciiMap) t).toUnicode_;
                    fromU = ((ConvTableAsciiMap) t).fromUnicode_;
                    sub = 0x1A;
                    kind = "A";
                } else {
                    System.err.println("skip " + ccsid + ": " + t.getClass().getName());
                    continue;
                }
                d.writeByte('S');
                d.writeByte(sub);
                for (int i = 0; i < 256; i++) d.writeChar(toU[i]);
                for (int i = 0; i < 65536; i++) d.writeByte(i < fromU.length ? fromU[i] : sub);
            }
            d.flush();
            Deflater def = new Deflater(Deflater.BEST_COMPRESSION);
            try (OutputStream fo = new DeflaterOutputStream(new FileOutputStream(new File(out, ccsid + ".bin")), def)) {
                fo.write(raw.toByteArray());
            }
            index.append(ccsid).append(' ').append(kind).append('\n');
        }
        try (FileWriter w = new FileWriter(new File(out, "index.txt"))) {
            w.write(index.toString());
        }
    }
}
